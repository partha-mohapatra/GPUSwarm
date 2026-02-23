# AWS Bootstrap Node Templates

## Files

- `infermeshd-bootstrap.cfn.yaml`: EC2 bootstrap node using existing VPC/Subnet/KeyPair
- `infermeshd-bootstrap-full.cfn.yaml`: creates VPC/Subnet/KeyPair if not provided
- `bootstrap.env.example`: deploy config template
- `manage-bootstrap.sh`: repeatable deploy/update/terminate script using env config
- `ssh-current-ip.sh`: enable/disable SSH ingress for your current public IP

## Repeatable Deploy (recommended)

```bash
cp deploy/aws/bootstrap.env.example deploy/aws/bootstrap.env
# edit deploy/aws/bootstrap.env
chmod +x deploy/aws/manage-bootstrap.sh
./deploy/aws/manage-bootstrap.sh deploy --env deploy/aws/bootstrap.env
./deploy/aws/manage-bootstrap.sh status --env deploy/aws/bootstrap.env
```

The script:

- uploads `dist/infermeshd-linux-amd64` to S3 (unless `INFERMESHD_DOWNLOAD_URL` is provided)
- deploys/updates CloudFormation from env values
- discovers `peer_id` from bootstrap node via SSM
- prints final `libp2p_bootstrap_peers` entry
- optionally updates your local config if `LOCAL_CONFIG_PATH` is set in env
- exports stack-created EC2 keypair private key to local `.pem` (`KEY_OUTPUT_DIR`, default `deploy/aws/keys`)

Terminate stack:

```bash
./deploy/aws/manage-bootstrap.sh terminate --env deploy/aws/bootstrap.env
```

Check stack status/events:

```bash
./deploy/aws/manage-bootstrap.sh status --env deploy/aws/bootstrap.env
```

Start a previously stopped instance:

```bash
./deploy/aws/manage-bootstrap.sh start-instance --env deploy/aws/bootstrap.env
```

Configure nightly auto-stop (UTC) and remove it:

```bash
./deploy/aws/manage-bootstrap.sh set-night-stop --env deploy/aws/bootstrap.env --hour 23 --minute 0
./deploy/aws/manage-bootstrap.sh clear-night-stop --env deploy/aws/bootstrap.env
```

## SSH keypair location

When stack creates a new keypair, `manage-bootstrap.sh deploy` fetches the private key from:

`/ec2/keypair/<KeyPairId>` (SSM Parameter Store, decrypted)

and writes:

`<KEY_OUTPUT_DIR>/<KeyName>.pem`

with `chmod 600`.

If you pass `EXISTING_KEY_NAME`, private key is not re-created and cannot be auto-exported unless it exists in SSM for that key id.

Optional existing resource params in env:

- `VpcId=vpc-...`
- `SubnetId=subnet-...`
- `ExistingKeyName=my-keypair`
- `SUBNET_AZ=us-east-1a` (optional; if omitted and `SubnetId` is empty, script auto-selects a supported AZ for `INSTANCE_TYPE`)

## Manual Deploy (direct CLI)

```bash
aws cloudformation deploy \
  --stack-name infermesh-bootstrap \
  --template-file deploy/aws/infermeshd-bootstrap-full.cfn.yaml \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    InfermeshdDownloadUrl=https://YOUR_ARTIFACT_HOST/infermeshd-linux-amd64
```

## Security Group SSH toggle by current IP

```bash
./deploy/aws/ssh-current-ip.sh enable
./deploy/aws/ssh-current-ip.sh status
./deploy/aws/ssh-current-ip.sh disable
```

The script auto-resolves `SecurityGroupId` from stack output (`infermesh-bootstrap` by default).
Optional overrides:

```bash
INFERMESH_STACK_NAME=infermesh-bootstrap ./deploy/aws/ssh-current-ip.sh enable --region us-east-1
INFERMESH_SG_ID=sg-xxxx ./deploy/aws/ssh-current-ip.sh enable --region us-east-1
```

## Verify daemon and logs

```bash
sudo systemctl status infermeshd
sudo journalctl -u infermeshd -f
sudo tail -f /var/log/infermeshai/infermeshd.log
```

## Get bootstrap multiaddr

```bash
grep -m1 'libp2p node started' /var/log/infermeshai/infermeshd.log
```

Use:

`/ip4/<PUBLIC_IP>/tcp/39001/p2p/<PEER_ID>`

in clients/providers under `libp2p_bootstrap_peers`.
