// Pre-sign-up Cognito trigger: auto-confirm every self-signup. Identities
// are device-generated and anonymous (no email/phone) — there is nothing
// to verify. Abuse is bounded elsewhere (per-user quotas, M7 DeviceCheck).
package main

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

func handle(_ context.Context, event events.CognitoEventUserPoolsPreSignup) (events.CognitoEventUserPoolsPreSignup, error) {
	event.Response.AutoConfirmUser = true
	event.Response.AutoVerifyEmail = false
	event.Response.AutoVerifyPhone = false
	return event, nil
}

func main() {
	lambda.Start(handle)
}
