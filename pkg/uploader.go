package pkg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

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

	latestCommits, err := u.getLatestCommits()
	if err != nil {
		return err
	}

	fmt.Println(latestCommits)

	u.getOutdated(ctx)
	return nil
}

// returns a map of project PIDs (gitlab_group/project_name) to latest commit on specified source branch
func (u *Uploader) getLatestCommits() (map[string]string, error) {
	latestCommits := make(map[string]string)
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

// NEXT: implement function to list keys within s3 bucket
// base64 decode and compare commit shas from keys against result of getLatestCommits()
// return slice of outdated PIDs that need to be cloned/uploaded
func (u *Uploader) getOutdated(ctx context.Context) ([]string, error) {
	res, err := u.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &u.bucket,
	})
	if err != nil {
		return nil, err
	}

	keys, err := processRawKeys(res)
	if err != nil {
		return nil, err
	}
	fmt.Println(keys)
	return nil, nil
}

type DecodedKey struct {
	Group       string `json:"group"`
	ProjectName string `json:"project_name"`
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
}

// processRawKeys processes response of ListObjectsV2 against aws api
// return is decoded/marshalled list of keys
// Context: within s3, our uploaded object keys are based64 encoded jsons
func processRawKeys(raw *s3.ListObjectsV2Output) ([]DecodedKey, error) {
	keys := []DecodedKey{}
	for _, obj := range raw.Contents {
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
		keys = append(keys, jsonKey)
	}
	return keys, nil
}
