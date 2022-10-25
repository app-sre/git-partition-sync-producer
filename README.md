# gitlab-sync-s3-push
Uploads encrypted/zipped latest versions of target GitLab projects to s3 bucket.  
This works in tandem with [gitlab-sync-s3-pull](https://github.com/dwelch0/gitlab-sync-s3-pull) to sync GitLab instances in isolated environments.

## Config File Format
Yaml file is an array of objects with following format:

```
source: 
  project_name
  namespace
  branch
destination:
  project_name
  namespace
  branch
```
## Environment Variables

### Required
* AWS_ACCESS_KEY_ID - s3 CRUD permissions required
* AWS_SECRET_ACCESS_KEY
* AWS_REGION
* AWS_S3_BUCKET - the name. not an ARN
* CONFIG_FILE_PATH - absolute path to yaml config file 
* GITLAB_BASE_URL - GitLab instance base url. Ex: https://gitlab.foobar.com
* GITLAB_USERNAME
* GITLAB_TOKEN - repository read permission required
* PUBLIC_KEY - value of x25519 format public key. See [age encryption](https://github.com/FiloSottile/age#readme)

### Optional
* RECONCILE_SLEEP_TIME - time between runs. defaults to 5 minutes (5m)
* WORKDIR - local directory where io operations will be performed

## Uploaded s3 Object Key Format
Uploaded keys are base64 encoded. Decoded, the key is a json string with following structure:
```
{
  "group":"some-gitlab-group",
  "project_name":"some-gitlab-project",
  "commit_sha":"full-commit-sha",
  "branch":"master"
}
```
**Note:** the values within each json will mirror values for each `destination` defined within config file (exluding `commit_sha` which is the latest commit pulled from `source`)