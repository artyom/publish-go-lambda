# publish-go-lambda

Command publish-go-lambda is a shortcut program that builds Go source in the
current directory and publishes it as an existing AWS Lambda (either Go 1.x or
custom Amazon Linux 2 runtime). It automatically discovers "handler name" from
AWS Lambda configuration.

AWS Lambda must either be configured with Go 1.x runtime (amd64 architecture
only), or the custom runtime on Amazon Linux 2 (“provided.al2”) for either
amd64 or arm64 architecture.

It is an equivalent of:

    # build program
    GOOS=linux GOARCH=amd64 go build -o main

    # create zip file with the proper handler name
    zip lambda.zip main

    # upload resulting zip to AWS Lambda

This program applies some safety checks by default: it checks that the main
package imports `github.com/aws/aws-lambda-go/lambda` dependency, and that
package documentation mentions (short) lambda name.

Call it with the full resource ARN:

    publish-go-lambda arn:aws:lambda:us-west-2:123456789012:function:my-function

partial ARN:

    publish-go-lambda 123456789012:function:my-function

or with just the short function name:

    publish-go-lambda my-function

This program requires permissions to [GetFunctionConfiguration] and
[UpdateFunctionCode] AWS APIs.

[GetFunctionConfiguration]: https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConfiguration.html
[UpdateFunctionCode]: https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionCode.html
