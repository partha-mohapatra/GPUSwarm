#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<USAGE
Usage:
  $0 enable  [--stack-name infermesh-bootstrap] [--region us-east-1] [--security-group-id sg-xxxx] [--port 22]
  $0 disable [--stack-name infermesh-bootstrap] [--region us-east-1] [--security-group-id sg-xxxx] [--port 22]
  $0 status  [--stack-name infermesh-bootstrap] [--region us-east-1] [--security-group-id sg-xxxx] [--port 22]

Description:
  Enable/disable SSH ingress for your CURRENT public IP on a security group.

Resolution order for Security Group:
  1) --security-group-id
  2) INFERMESH_SG_ID env var
  3) CloudFormation output SecurityGroupId from --stack-name (default infermesh-bootstrap)
USAGE
}

if [[ $# -lt 1 ]]; then
  usage
  exit 1
fi

ACTION="$1"
shift

SG_ID=""
REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-}}"
PORT="22"
STACK_NAME="${INFERMESH_STACK_NAME:-infermesh-bootstrap}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --security-group-id)
      SG_ID="$2"; shift 2 ;;
    --stack-name)
      STACK_NAME="$2"; shift 2 ;;
    --region)
      REGION="$2"; shift 2 ;;
    --port)
      PORT="$2"; shift 2 ;;
    *)
      echo "Unknown arg: $1" >&2
      usage
      exit 1 ;;
  esac
done

if [[ -z "$SG_ID" && -n "${INFERMESH_SG_ID:-}" ]]; then
  SG_ID="${INFERMESH_SG_ID}"
fi

AWS_ARGS=()
if [[ -n "$REGION" ]]; then
  AWS_ARGS+=(--region "$REGION")
fi

resolve_sg_id_from_stack() {
  aws cloudformation describe-stacks "${AWS_ARGS[@]}" \
    --stack-name "$STACK_NAME" \
    --query "Stacks[0].Outputs[?OutputKey=='SecurityGroupId'].OutputValue" \
    --output text 2>/dev/null || true
}

if [[ -z "$SG_ID" ]]; then
  SG_ID="$(resolve_sg_id_from_stack | tr -d '[:space:]')"
fi

if [[ -z "$SG_ID" || "$SG_ID" == "None" ]]; then
  echo "Could not resolve security group id." >&2
  echo "Set one of: --security-group-id, INFERMESH_SG_ID, or deploy stack '$STACK_NAME' with SecurityGroupId output." >&2
  exit 1
fi

CURRENT_IP="$(curl -fsSL https://checkip.amazonaws.com | tr -d '[:space:]')"
CIDR="${CURRENT_IP}/32"
DESC="infermesh-current-ip"

perm_json() {
  cat <<JSON
[
  {
    "IpProtocol": "tcp",
    "FromPort": ${PORT},
    "ToPort": ${PORT},
    "IpRanges": [
      {
        "CidrIp": "${CIDR}",
        "Description": "${DESC}"
      }
    ]
  }
]
JSON
}

case "$ACTION" in
  enable)
    set +e
    out=$(aws ec2 authorize-security-group-ingress "${AWS_ARGS[@]}" --group-id "$SG_ID" --ip-permissions "$(perm_json)" 2>&1)
    rc=$?
    set -e
    if [[ $rc -ne 0 ]]; then
      if echo "$out" | grep -qi 'InvalidPermission.Duplicate'; then
        echo "SSH already enabled for ${CIDR} on ${SG_ID}"
      else
        echo "$out" >&2
        exit $rc
      fi
    else
      echo "Enabled SSH for ${CIDR} on ${SG_ID}"
    fi
    ;;
  disable)
    set +e
    out=$(aws ec2 revoke-security-group-ingress "${AWS_ARGS[@]}" --group-id "$SG_ID" --ip-permissions "$(perm_json)" 2>&1)
    rc=$?
    set -e
    if [[ $rc -ne 0 ]]; then
      if echo "$out" | grep -qi 'InvalidPermission.NotFound'; then
        echo "SSH rule not present for ${CIDR} on ${SG_ID}"
      else
        echo "$out" >&2
        exit $rc
      fi
    else
      echo "Disabled SSH for ${CIDR} on ${SG_ID}"
    fi
    ;;
  status)
    aws ec2 describe-security-groups "${AWS_ARGS[@]}" --group-ids "$SG_ID" \
      --query "SecurityGroups[0].IpPermissions[?FromPort==\`${PORT}\` && ToPort==\`${PORT}\`].IpRanges[].CidrIp" --output text | tr '\t' '\n' | sed '/^$/d'
    ;;
  *)
    echo "Invalid action: $ACTION" >&2
    usage
    exit 1
    ;;
esac
