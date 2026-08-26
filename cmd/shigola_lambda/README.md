# shigola_lambda

Run shigola on AWS lambda. This implementation uses the native Go AWS Lambda rutime. There are a couple limitations to using lambda to run shigola:

- No connection pooling: The database connection will be rebuilt on every lambda request.

The following steps assume you have a working shigola configuration. 

## Creating a new lambda function

### From the AWS console:

- Navigate to lambda and click "Create function". We're going to "Author from scratch".
  - Name: name your function whatever you like.
  - Runtime: At the bottom of the drop down select "Provide your own bootstrap on Amazon Linux 2".
  - Architecture: Select your desired architecture. There's a shigola lambda version for both `x86_64` and `arm64` available.
  - Role: Depends on your environment. For the sake of this walk through we will "Create a new role from template"
  - Role Name: Up to you. `shigola` is a good role name. 
  - Policy Templates: Select "Basic Edge Lambda Permissions".
- Click "Create function".

## Preparing the function for upload

Lambda expects an archive with the function to be uploaded. In shigola's case we will be creating an archive of the binary `bootstrap` and a `config.toml` file:

```bash
zip deployment.zip bootstrap config.toml
```

*Note: shigola will check for a config file named `config.toml` by default. This can be changed by setting the environment variable `SHIGOLA_CONFIG`*

Back in the AWS console for the function that was created earlier, locate the section "Code" and click "Upload from" and choose ".zip file". Find and upload the `deployment.zip` archive you just created.

## Configuring the Lambda function

- Under "Basic Settings" 
  - Memory: Adjust to your preference. Depending on the config.toml file you may need more or less memory for the function. 
  - Timeout: Set to max time (5 minutes).

## Deployment

AWS Supports several ways to trigger lambda functions (i.e. API Gateway, ALB, Function URL). `shigola_lambda` uses the [algnhsa](github.com/akrylysov/algnhsa) package as the AWS lambda shim. Various [deployment configs are documented in the README](https://github.com/akrylysov/algnhsa#deployment) for `algnhsa`.

### Setting up API Gateway

When using API Gateway there are a few key details that need to be configured during setup:

* Under "Content Encoding" check the box to enable and set the value to 0. This is important as shigola will return tiles in gzip encoded. Without this checked the content encoding will be improperly handled. 
* Under "Binary Media Types" click "Add Binary Media Type".
* Input `*/*` as the value. This is necessary as shigola returns protocol buffers, which are a binary format. Without this configuration API gateway will return the vector tile payloads as base64 encoded strings.

## Building from source

Building `shigola_lambda` works the same way as building normal shigola with the exception that it must be built for linux. Navigate to the repository root then `cmd/shigola_lambda` and execute the following command to create an arm64 binary:

```console
GOARCH=arm64 GOOS=linux go build -tags lambda.norpc -o bootstrap main.go
```

A binary named `bootstrap` will be created.
