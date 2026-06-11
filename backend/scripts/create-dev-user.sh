#!/usr/bin/env bash
# Creates (or resets) the hardcoded M2 dev user in the Cognito pool and
# appends its credentials to secrets/dev.env. Idempotent.
set -euo pipefail

cd "$(dirname "$0")/.."
: "${AWS_PROFILE:=smachnogo}"
export AWS_PROFILE

POOL_ID=$(cd terraform/envs/dev && terraform output -raw cognito_pool_id)
CLIENT_ID=$(cd terraform/envs/dev && terraform output -raw cognito_client_id)
USERNAME="dev-user-1"
PASSWORD=$(openssl rand -hex 24) # 48 chars, > pool minimum 20

if aws cognito-idp admin-get-user --user-pool-id "$POOL_ID" --username "$USERNAME" >/dev/null 2>&1; then
  echo "user $USERNAME exists — resetting password"
else
  aws cognito-idp admin-create-user --user-pool-id "$POOL_ID" --username "$USERNAME" \
    --message-action SUPPRESS >/dev/null
  echo "user $USERNAME created"
fi
aws cognito-idp admin-set-user-password --user-pool-id "$POOL_ID" --username "$USERNAME" \
  --password "$PASSWORD" --permanent
SUB=$(aws cognito-idp admin-get-user --user-pool-id "$POOL_ID" --username "$USERNAME" \
  --query "UserAttributes[?Name=='sub'].Value" --output text)

# Rewrite the cognito block in secrets/dev.env
grep -v '^COGNITO_' secrets/dev.env > secrets/dev.env.tmp && mv secrets/dev.env.tmp secrets/dev.env
cat >> secrets/dev.env <<EOF
COGNITO_POOL_ID=$POOL_ID
COGNITO_CLIENT_ID=$CLIENT_ID
COGNITO_DEV_USERNAME=$USERNAME
COGNITO_DEV_PASSWORD=$PASSWORD
EOF
echo "pool=$POOL_ID client=$CLIENT_ID user=$USERNAME sub=$SUB"
echo "credentials appended to secrets/dev.env"
