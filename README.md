# gitlab-sync-push

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
* GITLAB_TOKEN - repository read permission required

### Optional
* RECONCILE_SLEEP_TIME - time between runs. defaults to 5 minutes (5m)