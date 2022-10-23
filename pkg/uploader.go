package pkg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

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

type Sync struct {
	Source      GitTarget `yaml:"source"`
	Destination GitTarget `yaml:"destination"`
}

type Uploader struct {
	syncs     []*Sync
	glClient  *gitlab.Client
	glBaseURL string
	s3Client  *s3.Client
	awsRegion string
	bucket    string
}

func NewUploader(rawCfg []byte, glToken, glURL, awsAccessKey, awsSecretKey, awsRegion, bucket string) (*Uploader, error) {
	var cfg []*Sync
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
func (u *Uploader) Run(ctx context.Context) error {
	log.Println("Starting run...")

	glCommits, err := u.getLatestGitlabCommits()
	if err != nil {
		return err
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	s3Commits, err := u.getS3Keys(ctxTimeout)
	if err != nil {
		return err
	}

	outdated, err := u.getOutdated(glCommits, s3Commits)
	if err != nil {
		return err
	}

	fmt.Println(outdated)
	return nil
}

// keys are gitlab PID (gitlab_group/project_name)
// values are commit SHA
type PidToCommit map[string]string

// returns a map of project PIDs (gitlab_group/project_name) to latest commit on specified source branch
func (u *Uploader) getLatestGitlabCommits() (PidToCommit, error) {
	latestCommits := make(PidToCommit)
	for _, sync := range u.syncs {
		pid := fmt.Sprintf("%s/%s", sync.Source.Group, sync.Source.ProjectName)
		// by default, the latest commit is returned
		commit, _, err := u.glClient.Commits.GetCommit(pid, sync.Source.Branch, nil)
		if err != nil {
			return nil, err
		}
		latestCommits[pid] = commit.ID
	}
	return latestCommits, nil
}

type DecodedKey struct {
	Group       string `json:"group"`
	ProjectName string `json:"project_name"`
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
}

// getS3Keys processes response of ListObjectsV2 against aws api
// return is decoded/marshalled list of keys
// Context: within s3, our uploaded object keys are based64 encoded jsons
func (u *Uploader) getS3Keys(ctx context.Context) (PidToCommit, error) {
	res, err := u.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &u.bucket,
	})
	if err != nil {
		return nil, err
	}

	s3PidCommits := make(PidToCommit)
	for _, obj := range res.Contents {
		// remove file extension before attempting decode
		// extension is .tar.gpg, split at first occurrence of .
		encodedKey := strings.SplitN(*obj.Key, ".", 2)[0]
		decodedBytes, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, err
		}
		var jsonKey DecodedKey
		err = json.Unmarshal(decodedBytes, &jsonKey)
		if err != nil {
			return nil, err
		}
		pid := fmt.Sprintf("%s/%s", jsonKey.Group, jsonKey.ProjectName)
		s3PidCommits[pid] = jsonKey.CommitSHA
	}
	return s3PidCommits, nil
}

// getOutdated iterates through desired Syncs (defined within config file)
// and compares existing latest commits on source GitLab projects against
// commits stored within s3 keys for corresponding destination GitLab projects
// return is Syncs that do not exist within s3Commits OR s3Commit != glCommit
func (u *Uploader) getOutdated(glCommits, s3Commits map[string]string) ([]*Sync, error) {
	outdated := []*Sync{}
	for _, sync := range u.syncs {
		sourcePid := fmt.Sprintf("%s/%s", sync.Source.Group, sync.Source.ProjectName)
		destinationPid := fmt.Sprintf("%s/%s", sync.Destination.Group, sync.Destination.ProjectName)
		s3Commit, exist := s3Commits[destinationPid]
		if !exist || s3Commit != glCommits[sourcePid] {
			outdated = append(outdated, sync)
		}

		// remove keys from s3 map while processing
		// if map is not empty at end, there are s3 keys that should be deleted
		// i.e removed from config yaml as targets
		if exist {
			delete(s3Commits, destinationPid)
		}
	}

	if len(s3Commits) > 0 {
		// call clean up function
		// (delete s3 objects)
	}

	return outdated, nil
}
