package pkg

import (
	"fmt"
	"log"

	"github.com/xanzy/go-gitlab"
	"gopkg.in/yaml.v3"
)

type GitTarget struct {
	Namespace   string `yaml:"namespace"`
	ProjectName string `yaml:"project_name"`
	Branch      string `yaml:"branch"`
}

type Sync struct {
	Source      GitTarget `yaml:"source"`
	Destination GitTarget `yaml:"destination"`
}

type Uploader struct {
	glToken      string
	glBaseURL    string
	awsAccessKey string
	awsSecretKey string
	awsRegion    string
	bucket       string
	syncs        []*Sync
	cache        map[string]string
}

func NewUploader(rawCfg []byte, glToken, glURL, awsAccessKey, awsSecretKey, awsRegion, bucket string) (*Uploader, error) {

	var cfg []*Sync
	err := yaml.Unmarshal(rawCfg, &cfg)
	if err != nil {
		return nil, err
	}

	return &Uploader{
		glToken:      glToken,
		glBaseURL:    glURL,
		awsAccessKey: awsAccessKey,
		awsSecretKey: awsSecretKey,
		awsRegion:    awsRegion,
		syncs:        cfg,
		cache:        make(map[string]string),
	}, nil
}

func (u *Uploader) Run() error {
	log.Println("Starting run...")

	gl, err := gitlab.NewClient(
		u.glToken, gitlab.WithBaseURL(fmt.Sprintf("%s/api/v4", u.glBaseURL)))
	if err != nil {
		return err
	}

	latestCommits, err := u.getLatestCommits(gl)
	if err != nil {
		return err
	}

	fmt.Println(latestCommits)
	/*
		awsS3 := s3.New(s3.Options{
			Region: u.awsRegion,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
				u.awsAccessKey,
				u.awsSecretKey,
				"",
			)),
		})
	*/
	log.Println("Run completed")

	return nil
}

// returns a map of git repo to latest commit on specified source branch
func (u *Uploader) getLatestCommits(gl *gitlab.Client) (map[string]string, error) {
	latestCommits := make(map[string]string)
	for _, sync := range u.syncs {
		pid := fmt.Sprintf("%s/%s", sync.Source.Namespace, sync.Source.ProjectName)
		// by default, the latest commit is returned
		commit, _, err := gl.Commits.GetCommit(pid, sync.Source.Branch, nil)
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
