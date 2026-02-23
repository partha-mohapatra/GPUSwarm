#!/usr/bin/env bash
if [ -z "${BASH_VERSION:-}" ]; then
  exec /usr/bin/env bash "$0" "$@"
fi
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  ./deploy/aws/manage-bootstrap.sh deploy [--env deploy/aws/bootstrap.env]
  ./deploy/aws/manage-bootstrap.sh terminate [--env deploy/aws/bootstrap.env]
  ./deploy/aws/manage-bootstrap.sh status [--env deploy/aws/bootstrap.env]
  ./deploy/aws/manage-bootstrap.sh start-instance [--env deploy/aws/bootstrap.env]
  ./deploy/aws/manage-bootstrap.sh set-night-stop [--env deploy/aws/bootstrap.env] [--hour 23] [--minute 0]
  ./deploy/aws/manage-bootstrap.sh clear-night-stop [--env deploy/aws/bootstrap.env]

Description:
  deploy:    Deploys/updates infermesh bootstrap stack from env config, uploads infermeshd
             artifact if needed, then discovers peer_id and prints bootstrap peer entry.
  terminate: Deletes the bootstrap CloudFormation stack and waits for completion.
  status:    Prints stack status and recent CloudFormation events for troubleshooting.
  start-instance: Starts the existing EC2 instance in the stack and waits until running.
  set-night-stop: Configures nightly UTC stop on the instance via cron + graceful daemon stop.
  clear-night-stop: Removes nightly stop cron entry from the instance.
USAGE
}

ENV_FILE="${SCRIPT_DIR}/bootstrap.env"
ACTION="${1:-}"
if [[ "$ACTION" == "-h" || "$ACTION" == "--help" ]]; then
  usage
  exit 0
fi
if [[ -z "$ACTION" ]]; then
  usage
  exit 1
fi
shift

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env)
      ENV_FILE="$2"; shift 2 ;;
    --hour)
      NIGHT_STOP_HOUR="$2"; shift 2 ;;
    --minute)
      NIGHT_STOP_MINUTE="$2"; shift 2 ;;
    -h|--help)
      usage
      exit 0 ;;
    *)
      echo "Unknown arg: $1" >&2
      usage
      exit 1 ;;
  esac
done

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Env file not found: $ENV_FILE" >&2
  echo "Create it from deploy/aws/bootstrap.env.example" >&2
  exit 1
fi

if [[ "$ENV_FILE" != /* ]]; then
  ENV_FILE="$(pwd)/$ENV_FILE"
fi
ENV_DIR="$(cd "$(dirname "$ENV_FILE")" && pwd)"

set -a
source "$ENV_FILE"
set +a

AWS_PROFILE="${AWS_PROFILE:-default}"
AWS_REGION="${AWS_REGION:-us-east-1}"
STACK_NAME="${STACK_NAME:-infermesh-bootstrap}"
TEMPLATE_FILE="${TEMPLATE_FILE:-deploy/aws/infermeshd-bootstrap-full.cfn.yaml}"
BINARY_PATH="${BINARY_PATH:-dist/infermeshd-linux-amd64}"
PRESIGN_TTL="${PRESIGN_TTL:-604800}"
LIBP2P_PORT="${LIBP2P_PORT:-39001}"
EXPORT_CREATED_KEYPAIR="${EXPORT_CREATED_KEYPAIR:-true}"
KEY_OUTPUT_DIR="${KEY_OUTPUT_DIR:-${SCRIPT_DIR}/keys}"
SUBNET_AZ="${SUBNET_AZ:-}"

AWS_ARGS=(--profile "$AWS_PROFILE" --region "$AWS_REGION")
NIGHT_STOP_HOUR="${NIGHT_STOP_HOUR:-${NIGHTLY_STOP_UTC_HOUR:-23}}"
NIGHT_STOP_MINUTE="${NIGHT_STOP_MINUTE:-${NIGHTLY_STOP_UTC_MINUTE:-0}}"

resolve_existing_path() {
  local p="$1"
  local base
  if [[ "$p" == /* && -e "$p" ]]; then
    echo "$p"
    return 0
  fi
  for base in "$(pwd)" "$ENV_DIR" "$REPO_ROOT" "$SCRIPT_DIR"; do
    if [[ -e "${base}/${p}" ]]; then
      echo "${base}/${p}"
      return 0
    fi
  done
  echo "$p"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command missing: $1" >&2
    exit 1
  fi
}

stack_output() {
  local key="$1"
  aws cloudformation describe-stacks "${AWS_ARGS[@]}" \
    --stack-name "$STACK_NAME" \
    --query "Stacks[0].Outputs[?OutputKey=='${key}'].OutputValue | [0]" \
    --output text
}

stack_exists() {
  local s
  s="$(aws cloudformation describe-stacks "${AWS_ARGS[@]}" --stack-name "$STACK_NAME" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || true)"
  [[ -n "$s" && "$s" != "None" ]]
}

stack_instance_id() {
  trim_ws "$(stack_output InstanceId)"
}

instance_public_ip() {
  local instance_id="$1"
  aws ec2 describe-instances "${AWS_ARGS[@]}" \
    --instance-ids "$instance_id" \
    --query "Reservations[0].Instances[0].PublicIpAddress" \
    --output text 2>/dev/null || true
}

run_ssm_commands() {
  local instance_id="$1"
  local commands_payload="$2"
  local cmd_id
  local status
  cmd_id="$(aws ssm send-command "${AWS_ARGS[@]}" \
    --instance-ids "$instance_id" \
    --document-name AWS-RunShellScript \
    --parameters "$commands_payload" \
    --query "Command.CommandId" --output text 2>/dev/null || true)"
  if [[ -z "$cmd_id" || "$cmd_id" == "None" ]]; then
    return 1
  fi
  aws ssm wait command-executed "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" >/dev/null 2>&1 || true
  status="$(aws ssm get-command-invocation "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" --query "Status" --output text 2>/dev/null || true)"
  aws ssm get-command-invocation "${AWS_ARGS[@]}" \
    --command-id "$cmd_id" \
    --instance-id "$instance_id" \
    --query "[Status,StandardOutputContent,StandardErrorContent]" \
    --output text 2>/dev/null || true
  [[ "$status" == "Success" ]]
}

print_stack_diagnostics() {
  echo "=== Stack status: ${STACK_NAME} ==="
  aws cloudformation describe-stacks "${AWS_ARGS[@]}" \
    --stack-name "$STACK_NAME" \
    --query "Stacks[0].[StackStatus,StackStatusReason,LastUpdatedTime,CreationTime]" \
    --output table || true
  echo
  echo "=== Failed events (root-cause first) ==="
  aws cloudformation describe-stack-events "${AWS_ARGS[@]}" \
    --stack-name "$STACK_NAME" \
    --query "StackEvents[?contains(ResourceStatus, 'FAILED')].[Timestamp,LogicalResourceId,ResourceStatus,ResourceStatusReason]" \
    --output table || true
  echo
  echo "=== Recent stack events ==="
  aws cloudformation describe-stack-events "${AWS_ARGS[@]}" \
    --stack-name "$STACK_NAME" \
    --max-items 60 \
    --query "StackEvents[].[Timestamp,LogicalResourceId,ResourceStatus,ResourceStatusReason]" \
    --output table || true
}

trim_ws() {
  echo "$1" | tr -d '[:space:]'
}

validate_night_time() {
  if ! [[ "$NIGHT_STOP_HOUR" =~ ^[0-9]+$ ]] || (( NIGHT_STOP_HOUR < 0 || NIGHT_STOP_HOUR > 23 )); then
    echo "Invalid --hour (UTC): ${NIGHT_STOP_HOUR}. Expected 0..23" >&2
    exit 1
  fi
  if ! [[ "$NIGHT_STOP_MINUTE" =~ ^[0-9]+$ ]] || (( NIGHT_STOP_MINUTE < 0 || NIGHT_STOP_MINUTE > 59 )); then
    echo "Invalid --minute (UTC): ${NIGHT_STOP_MINUTE}. Expected 0..59" >&2
    exit 1
  fi
}

maybe_export_keypair() {
  local key_name="$1"
  if [[ "${EXPORT_CREATED_KEYPAIR}" != "true" ]]; then
    return 0
  fi
  if [[ -z "$key_name" || "$key_name" == "None" ]]; then
    return 0
  fi

  local key_id
  local param_name
  local key_material
  local out_file

  key_id="$(aws ec2 describe-key-pairs "${AWS_ARGS[@]}" --key-names "$key_name" --query 'KeyPairs[0].KeyPairId' --output text 2>/dev/null || true)"
  key_id="$(trim_ws "$key_id")"
  if [[ -z "$key_id" || "$key_id" == "None" ]]; then
    return 0
  fi

  param_name="/ec2/keypair/${key_id}"
  key_material="$(aws ssm get-parameter "${AWS_ARGS[@]}" --name "$param_name" --with-decryption --query 'Parameter.Value' --output text 2>/dev/null || true)"
  if [[ -z "$key_material" || "$key_material" == "None" ]]; then
    return 0
  fi

  mkdir -p "$KEY_OUTPUT_DIR"
  out_file="${KEY_OUTPUT_DIR}/${key_name}.pem"
  if [[ -f "$out_file" ]]; then
    return 0
  fi

  printf '%s\n' "$key_material" > "$out_file"
  chmod 600 "$out_file"
  EXPORTED_KEY_FILE="$out_file"
}

update_local_config() {
  local config_path="$1"
  local multiaddr="$2"
  if [[ -z "$config_path" ]]; then
    return 0
  fi
  if [[ ! -f "$config_path" ]]; then
    echo "LOCAL_CONFIG_PATH does not exist: $config_path" >&2
    return 1
  fi

  local tmp
  tmp="$(mktemp)"
  awk -v peer="$multiaddr" '
    BEGIN { replaced=0; skip_list=0 }
    {
      if (skip_list == 1) {
        if ($0 ~ /^  - /) { next }
        if ($0 ~ /^$/) { print; next }
        skip_list=0
      }
      if (replaced == 0 && $0 ~ /^libp2p_bootstrap_peers:/) {
        print "libp2p_bootstrap_peers:"
        print "  - \"" peer "\""
        replaced=1
        skip_list=1
        next
      }
      print
    }
    END {
      if (replaced == 0) {
        print "libp2p_bootstrap_peers:"
        print "  - \"" peer "\""
      }
    }
  ' "$config_path" > "$tmp"
  mv "$tmp" "$config_path"
}

discover_peer_id() {
  local instance_id="$1"
  local attempt
  local cmd_id
  local out
  for attempt in $(seq 1 30); do
    set +e
    cmd_id="$(aws ssm send-command "${AWS_ARGS[@]}" \
      --instance-ids "$instance_id" \
      --document-name AWS-RunShellScript \
      --comment "infermesh bootstrap peer id discovery" \
      --parameters 'commands=["grep -Eo \"12D3Koo[[:alnum:]]+\" /var/log/infermeshai/infermeshd.log | head -n1 || true"]' \
      --query "Command.CommandId" --output text 2>/dev/null)"
    rc=$?
    set -e
    if [[ $rc -ne 0 || -z "$cmd_id" || "$cmd_id" == "None" ]]; then
      sleep 5
      continue
    fi

    set +e
    aws ssm wait command-executed "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" >/dev/null 2>&1
    out="$(aws ssm get-command-invocation "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" --query "StandardOutputContent" --output text 2>/dev/null)"
    set -e
    out="$(trim_ws "$out")"
    if [[ "$out" =~ ^12D3Koo[[:alnum:]]+$ ]]; then
      echo "$out"
      return 0
    fi
    sleep 5
  done
  return 1
}

verify_instance_bootstrap() {
  local instance_id="$1"
  local cmd_id
  local status
  local out
  local out_cmd_id
  cmd_id="$(aws ssm send-command "${AWS_ARGS[@]}" \
    --instance-ids "$instance_id" \
    --document-name AWS-RunShellScript \
    --comment "infermesh bootstrap verification" \
    --parameters 'commands=["set -e","test -f /etc/infermeshai/config.yaml","systemctl list-unit-files | grep -q \"^infermeshd.service\"","systemctl is-active --quiet infermeshd"]' \
    --query "Command.CommandId" --output text 2>/dev/null || true)"
  if [[ -z "$cmd_id" || "$cmd_id" == "None" ]]; then
    echo "Unable to run bootstrap verification command over SSM."
    return 1
  fi

  set +e
  aws ssm wait command-executed "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" >/dev/null 2>&1
  status="$(aws ssm get-command-invocation "${AWS_ARGS[@]}" --command-id "$cmd_id" --instance-id "$instance_id" --query "Status" --output text 2>/dev/null)"
  set -e
  if [[ "$status" == "Success" ]]; then
    return 0
  fi

  echo "Bootstrap verification failed on ${instance_id}. Cloud-init diagnostics:"
  out_cmd_id="$(aws ssm send-command "${AWS_ARGS[@]}" \
    --instance-ids "$instance_id" \
    --document-name AWS-RunShellScript \
    --comment "infermesh bootstrap diagnostics" \
    --parameters 'commands=["set +e","systemctl status infermeshd --no-pager -l || true","echo ---","tail -n 120 /var/log/cloud-init-output.log || true","echo ---","tail -n 120 /var/log/cloud-init.log || true"]' \
    --query "Command.CommandId" --output text 2>/dev/null || true)"
  if [[ -n "$out_cmd_id" && "$out_cmd_id" != "None" ]]; then
    sleep 4
    out="$(aws ssm get-command-invocation "${AWS_ARGS[@]}" --command-id "$out_cmd_id" --instance-id "$instance_id" --query "StandardOutputContent" --output text 2>/dev/null || true)"
    if [[ -n "$out" ]]; then
      echo "$out"
    fi
  fi
  return 1
}

build_download_url() {
  if [[ -n "${INFERMESHD_DOWNLOAD_URL:-}" ]]; then
    echo "$INFERMESHD_DOWNLOAD_URL"
    return 0
  fi

  if [[ ! -f "$BINARY_PATH" ]]; then
    echo "Binary not found: $BINARY_PATH" >&2
    exit 1
  fi

  local account_id
  local bucket
  account_id="$(aws sts get-caller-identity "${AWS_ARGS[@]}" --query Account --output text)"
  bucket="${S3_BUCKET:-infermesh-bootstrap-artifacts-${account_id}-${AWS_REGION}}"

  if ! aws s3api head-bucket --bucket "$bucket" "${AWS_ARGS[@]}" >/dev/null 2>&1; then
    if [[ "$AWS_REGION" == "us-east-1" ]]; then
      aws s3api create-bucket --bucket "$bucket" "${AWS_ARGS[@]}" >/dev/null
    else
      aws s3api create-bucket --bucket "$bucket" "${AWS_ARGS[@]}" \
        --create-bucket-configuration "LocationConstraint=$AWS_REGION" >/dev/null
    fi
  fi

  aws s3 cp "$BINARY_PATH" "s3://${bucket}/infermeshd-linux-amd64" "${AWS_ARGS[@]}" >/dev/null
  aws s3 presign "s3://${bucket}/infermeshd-linux-amd64" --expires-in "$PRESIGN_TTL" "${AWS_ARGS[@]}"
}

resolve_supported_az() {
  local instance_type="$1"
  local az
  az="$(aws ec2 describe-instance-type-offerings "${AWS_ARGS[@]}" \
    --location-type availability-zone \
    --filters "Name=instance-type,Values=${instance_type}" \
    --query "InstanceTypeOfferings[0].Location" \
    --output text 2>/dev/null || true)"
  az="$(trim_ws "$az")"
  if [[ -z "$az" || "$az" == "None" ]]; then
    return 1
  fi
  echo "$az"
}

require_cmd aws
require_cmd awk
require_cmd grep

TEMPLATE_FILE="$(resolve_existing_path "$TEMPLATE_FILE")"
BINARY_PATH="$(resolve_existing_path "$BINARY_PATH")"
if [[ -n "${LOCAL_CONFIG_PATH:-}" ]]; then
  LOCAL_CONFIG_PATH="$(resolve_existing_path "$LOCAL_CONFIG_PATH")"
fi

if [[ "$ACTION" == "terminate" ]]; then
  STACK_STATUS="$(aws cloudformation describe-stacks "${AWS_ARGS[@]}" --stack-name "$STACK_NAME" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || true)"
  if [[ -z "$STACK_STATUS" || "$STACK_STATUS" == "None" ]]; then
    echo "Stack not found: $STACK_NAME"
    exit 0
  fi
  echo "Deleting stack: $STACK_NAME (${STACK_STATUS})"
  aws cloudformation delete-stack "${AWS_ARGS[@]}" --stack-name "$STACK_NAME"
  aws cloudformation wait stack-delete-complete "${AWS_ARGS[@]}" --stack-name "$STACK_NAME"
  echo "Stack deleted: $STACK_NAME"
  exit 0
fi

if [[ "$ACTION" == "status" ]]; then
  STACK_STATUS="$(aws cloudformation describe-stacks "${AWS_ARGS[@]}" --stack-name "$STACK_NAME" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || true)"
  if [[ -z "$STACK_STATUS" || "$STACK_STATUS" == "None" ]]; then
    echo "Stack not found: $STACK_NAME"
    exit 1
  fi
  print_stack_diagnostics
  exit 0
fi

if [[ "$ACTION" == "start-instance" ]]; then
  if ! stack_exists; then
    echo "Stack not found: $STACK_NAME" >&2
    exit 1
  fi
  INSTANCE_ID="$(stack_instance_id)"
  if [[ -z "$INSTANCE_ID" || "$INSTANCE_ID" == "None" ]]; then
    echo "Unable to resolve InstanceId from stack outputs." >&2
    exit 1
  fi
  echo "Starting instance: ${INSTANCE_ID}"
  aws ec2 start-instances "${AWS_ARGS[@]}" --instance-ids "$INSTANCE_ID" >/dev/null
  aws ec2 wait instance-running "${AWS_ARGS[@]}" --instance-ids "$INSTANCE_ID"
  PUBLIC_IP="$(trim_ws "$(instance_public_ip "$INSTANCE_ID")")"
  echo "Instance running: ${INSTANCE_ID}"
  echo "Public IP: ${PUBLIC_IP:-<pending>}"
  exit 0
fi

if [[ "$ACTION" == "set-night-stop" ]]; then
  if ! stack_exists; then
    echo "Stack not found: $STACK_NAME" >&2
    exit 1
  fi
  validate_night_time
  INSTANCE_ID="$(stack_instance_id)"
  if [[ -z "$INSTANCE_ID" || "$INSTANCE_ID" == "None" ]]; then
    echo "Unable to resolve InstanceId from stack outputs." >&2
    exit 1
  fi
  echo "Configuring nightly stop for ${INSTANCE_ID} at ${NIGHT_STOP_HOUR}:$(printf '%02d' "$NIGHT_STOP_MINUTE") UTC"
  SSM_OUT="$(run_ssm_commands "$INSTANCE_ID" "commands=[\"set -e\",\"sudo mkdir -p /etc/cron.d\",\"sudo bash -lc 'printf \\\"%s\\\\n\\\" \\\"SHELL=/bin/bash\\\" \\\"PATH=/sbin:/bin:/usr/sbin:/usr/bin\\\" \\\"${NIGHT_STOP_MINUTE} ${NIGHT_STOP_HOUR} * * * root /usr/bin/systemctl stop infermeshd || true; /usr/sbin/shutdown -h now\\\" > /etc/cron.d/infermesh-auto-stop'\",\"sudo chmod 644 /etc/cron.d/infermesh-auto-stop\",\"sudo systemctl restart crond || true\",\"sudo cat /etc/cron.d/infermesh-auto-stop\"]")" || true
  if [[ -z "$SSM_OUT" ]] || ! grep -q "^Success" <<<"$SSM_OUT"; then
    echo "Failed to configure nightly stop via SSM (instance may be stopped or SSM unavailable)." >&2
    [[ -n "${SSM_OUT:-}" ]] && echo "$SSM_OUT" >&2
    exit 1
  fi
  echo "$SSM_OUT"
  echo "Nightly stop configured."
  exit 0
fi

if [[ "$ACTION" == "clear-night-stop" ]]; then
  if ! stack_exists; then
    echo "Stack not found: $STACK_NAME" >&2
    exit 1
  fi
  INSTANCE_ID="$(stack_instance_id)"
  if [[ -z "$INSTANCE_ID" || "$INSTANCE_ID" == "None" ]]; then
    echo "Unable to resolve InstanceId from stack outputs." >&2
    exit 1
  fi
  echo "Removing nightly stop for ${INSTANCE_ID}"
  SSM_OUT="$(run_ssm_commands "$INSTANCE_ID" "commands=[\"set -e\",\"sudo rm -f /etc/cron.d/infermesh-auto-stop\",\"sudo systemctl restart crond || true\",\"sudo test ! -f /etc/cron.d/infermesh-auto-stop && echo removed\"]")" || true
  if [[ -z "$SSM_OUT" ]] || ! grep -q "^Success" <<<"$SSM_OUT"; then
    echo "Failed to clear nightly stop via SSM (instance may be stopped or SSM unavailable)." >&2
    [[ -n "${SSM_OUT:-}" ]] && echo "$SSM_OUT" >&2
    exit 1
  fi
  echo "$SSM_OUT"
  echo "Nightly stop removed."
  exit 0
fi

if [[ "$ACTION" != "deploy" ]]; then
  echo "Invalid action: $ACTION" >&2
  usage
  exit 1
fi

if [[ ! -f "$TEMPLATE_FILE" ]]; then
  echo "Template not found: $TEMPLATE_FILE" >&2
  exit 1
fi

echo "Preparing download URL..."
DOWNLOAD_URL="$(build_download_url)"

echo "Checking stack state..."
STACK_STATUS="$(aws cloudformation describe-stacks "${AWS_ARGS[@]}" --stack-name "$STACK_NAME" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || true)"
if [[ "$STACK_STATUS" == "ROLLBACK_COMPLETE" ]]; then
  echo "Deleting failed stack state: $STACK_NAME"
  aws cloudformation delete-stack "${AWS_ARGS[@]}" --stack-name "$STACK_NAME"
  aws cloudformation wait stack-delete-complete "${AWS_ARGS[@]}" --stack-name "$STACK_NAME"
fi

PARAMS=("InfermeshdDownloadUrl=${DOWNLOAD_URL}" "Libp2pPort=${LIBP2P_PORT}")
[[ -n "${INSTANCE_TYPE:-}" ]] && PARAMS+=("InstanceType=${INSTANCE_TYPE}")
[[ -n "${ALLOWED_SSH_CIDR:-}" ]] && PARAMS+=("AllowedSshCidr=${ALLOWED_SSH_CIDR}")
[[ -n "${RENDEZVOUS:-}" ]] && PARAMS+=("Rendezvous=${RENDEZVOUS}")
[[ -n "${BOOTSTRAP_PEERS:-}" ]] && PARAMS+=("BootstrapPeers=${BOOTSTRAP_PEERS}")
[[ -n "${VPC_ID:-}" ]] && PARAMS+=("VpcId=${VPC_ID}")
[[ -n "${SUBNET_ID:-}" ]] && PARAMS+=("SubnetId=${SUBNET_ID}")
[[ -n "${EXISTING_KEY_NAME:-}" ]] && PARAMS+=("ExistingKeyName=${EXISTING_KEY_NAME}")
[[ -n "${KEY_PAIR_NAME:-}" ]] && PARAMS+=("KeyPairName=${KEY_PAIR_NAME}")
[[ -n "${AMI_ID:-}" ]] && PARAMS+=("AmiId=${AMI_ID}")

# If stack will create subnet and no AZ is provided, auto-pick a supported AZ only on fresh create.
if [[ -z "${SUBNET_ID:-}" && -z "${SUBNET_AZ:-}" && ( -z "${STACK_STATUS:-}" || "${STACK_STATUS}" == "ROLLBACK_COMPLETE" ) ]]; then
  AUTO_AZ="$(resolve_supported_az "${INSTANCE_TYPE:-t3.micro}" || true)"
  if [[ -n "$AUTO_AZ" ]]; then
    SUBNET_AZ="$AUTO_AZ"
    echo "Auto-selected subnet AZ for instance type ${INSTANCE_TYPE:-t3.micro}: ${SUBNET_AZ}"
  fi
fi
[[ -n "${SUBNET_AZ:-}" ]] && PARAMS+=("SubnetAvailabilityZone=${SUBNET_AZ}")

echo "Deploying stack: $STACK_NAME"
set +e
aws cloudformation deploy "${AWS_ARGS[@]}" \
  --stack-name "$STACK_NAME" \
  --template-file "$TEMPLATE_FILE" \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides "${PARAMS[@]}"
DEPLOY_RC=$?
set -e
if [[ $DEPLOY_RC -ne 0 ]]; then
  echo "Deployment failed for stack: $STACK_NAME"
  print_stack_diagnostics
  exit $DEPLOY_RC
fi

INSTANCE_ID="$(trim_ws "$(stack_output InstanceId)")"
PUBLIC_IP="$(trim_ws "$(stack_output PublicIp)")"
SG_ID="$(trim_ws "$(stack_output SecurityGroupId)")"
VPC_ID_OUT="$(trim_ws "$(stack_output VpcId)")"
SUBNET_ID_OUT="$(trim_ws "$(stack_output SubnetId)")"
KEY_NAME_OUT="$(trim_ws "$(stack_output KeyName)")"

echo "Verifying bootstrap on instance: $INSTANCE_ID"
if ! verify_instance_bootstrap "$INSTANCE_ID"; then
  exit 1
fi

echo "Discovering peer_id via SSM (instance: $INSTANCE_ID)..."
PEER_ID="$(discover_peer_id "$INSTANCE_ID" || true)"
if [[ -z "$PEER_ID" ]]; then
  echo "WARNING: peer_id not yet available from logs. Retry with:" >&2
  echo "  aws ssm send-command ${AWS_ARGS[*]} --instance-ids $INSTANCE_ID --document-name AWS-RunShellScript --parameters 'commands=[\"grep -Eo \\\"12D3Koo[[:alnum:]]+\\\" /var/log/infermeshai/infermeshd.log | head -n1 || true\"]'" >&2
  BOOTSTRAP_MULTIADDR="/ip4/${PUBLIC_IP}/tcp/${LIBP2P_PORT}/p2p/<PEER_ID_FROM_LOG>"
else
  BOOTSTRAP_MULTIADDR="/ip4/${PUBLIC_IP}/tcp/${LIBP2P_PORT}/p2p/${PEER_ID}"
fi

if [[ -n "${LOCAL_CONFIG_PATH:-}" ]]; then
  update_local_config "$LOCAL_CONFIG_PATH" "$BOOTSTRAP_MULTIADDR"
fi

EXPORTED_KEY_FILE=""
maybe_export_keypair "$KEY_NAME_OUT"

cat <<OUT

Deployment complete.

Stack:             ${STACK_NAME}
Region:            ${AWS_REGION}
InstanceId:        ${INSTANCE_ID}
PublicIp:          ${PUBLIC_IP}
SecurityGroupId:   ${SG_ID}
VpcId:             ${VPC_ID_OUT}
SubnetId:          ${SUBNET_ID_OUT}
KeyName:           ${KEY_NAME_OUT}
KeyFile:           ${EXPORTED_KEY_FILE:-<not-exported>}
PeerId:            ${PEER_ID:-<pending>}
BootstrapMultiaddr:${BOOTSTRAP_MULTIADDR}

Use in infermeshd config:
libp2p_bootstrap_peers:
  - "${BOOTSTRAP_MULTIADDR}"

OUT
