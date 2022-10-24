package pkg

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/xanzy/go-gitlab"

	"gopkg.in/yaml.v3"
)

type GitTarget struct {
	Group       string `yaml:"group"`
	ProjectName string `yaml:"project_name"`
	Branch      string `yaml:"branch"`
}

type GitSync struct {
	Source      GitTarget `yaml:"source"`
	Destination GitTarget `yaml:"destination"`
}

type Uploader struct {
	syncs     []*GitSync
	glClient  *gitlab.Client
	glBaseURL string
	s3Client  *s3.Client
	awsRegion string
	bucket    string
}

func NewUploader(rawCfg []byte, glToken, glURL, awsAccessKey, awsSecretKey, awsRegion, bucket string) (*Uploader, error) {
	var cfg []*GitSync
	err := yaml.Unmarshal(rawCfg, &cfg)
	if err != nil {
		return nil, err
	}

	gl, err := gitlab.NewClient(
		glToken, gitlab.WithBaseURL(fmt.Sprintf("%s/api/v4", glURL)))
	if err != nil {
		return nil, err
	}

	awsS3 := s3.New(s3.Options{
		Region: awsRegion,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			awsAccessKey,
			awsSecretKey,
			"",
		)),
	})

	return &Uploader{
		syncs:     cfg,
		glClient:  gl,
		glBaseURL: glURL,
		s3Client:  awsS3,
		awsRegion: awsRegion,
		bucket:    bucket,
	}, nil
}

// Run executes steps to reconcile s3 bucket with existing state of gitlab projects
func (u *Uploader) Run(ctx context.Context, dryRun bool) error {
	log.Println("Starting run...")

	glCommits, err := u.getLatestGitlabCommits()
	if err != nil {
		return err
	}

	s3ObjectInfos, err := u.getS3Keys(ctx)
	if err != nil {
		return err
	}

	toUpdate, toDelete, err := u.getOutOfSync(ctx, glCommits, s3ObjectInfos)
	if err != nil {
		return err
	}

	if dryRun {
		for _, update := range toUpdate {
			fmt.Println(fmt.Sprintf("[DRY RUN] s3 object for destination PID `%s/%s` will be updated",
				update.Destination.Group,
				update.Destination.ProjectName))
		}
		for _, delete := range toDelete {
			fmt.Println(fmt.Sprintf("[DRY RUN] s3 object with key `%s` will be deleted", *delete))
		}
		return nil
	}

	err = u.removeOutdated(ctx, toDelete)
	fmt.Println(toUpdate)
	return nil
}

type DecodedKey struct {
	Group       string `json:"group"`
	ProjectName string `json:"project_name"`
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
}

// iterates through desired Syncs (defined within config file)
// and compares latest commits on source GitLab projects against
// commits stored within s3 keys for corresponding destination GitLab projects
// return is slice of Sync that do not exist within s3Commits OR s3Commit != glCommit
// and slice of s3 object keys to delete
func (u *Uploader) getOutOfSync(ctx context.Context, glCommits map[string]string,
	objInfos map[string]*s3ObjectInfo) ([]*GitSync, []*string, error) {

	outdated := []*GitSync{}
	toDelete := []*string{}
	for _, sync := range u.syncs {
		sourcePid := fmt.Sprintf("%s/%s", sync.Source.Group, sync.Source.ProjectName)
		destinationPid := fmt.Sprintf("%s/%s", sync.Destination.Group, sync.Destination.ProjectName)

		objInfo, exist := objInfos[destinationPid]
		if !exist {
			// new target added to config file
			outdated = append(outdated, sync)
		} else if objInfo.CommitSHA != glCommits[sourcePid] {
			fmt.Println(objInfo.CommitSHA)
			fmt.Println(glCommits[sourcePid])
			// existing target is out of date
			outdated = append(outdated, sync)
			toDelete = append(toDelete, objInfo.Key)

			delete(objInfos, destinationPid) // remove processed keys from s3 bucket map
		} else {
			// target is up to date
			delete(objInfos, destinationPid)
		}
	}

	// if map is not empty at end, there are s3 keys that should be deleted
	// i.e removed from config file as targets
	for _, obj := range objInfos {
		toDelete = append(toDelete, obj.Key)
	}

	return outdated, toDelete, nil
}
