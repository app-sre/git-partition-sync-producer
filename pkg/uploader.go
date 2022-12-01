package pkg

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/machinebox/graphql"
	"github.com/xanzy/go-gitlab"
	"gopkg.in/yaml.v3"
)

type Uploader struct {
	awsRegion  string
	bucket     string
	glBaseURL  string
	glUsername string
	glToken    string
	publicKey  string
	workdir    string

	glClient *gitlab.Client
	s3Client *s3.Client

	syncs []*SyncConfig
}

type Apps struct {
	CodeComponentGitSyncs []CodeComponents `yaml:"apps_v1"`
}

type CodeComponents struct {
	GitlabSyncs []GitlabSync `yaml:"codeComponents"`
}

type GitlabSync struct {
	GitSync *SyncConfig `yaml:"gitlabSync"`
}

type SyncConfig struct {
	Source      GitTarget `yaml:"sourceProject"`
	Destination GitTarget `yaml:"destinationProject"`
	repoPath    string
	tarPath     string
	encryptPath string
}

type GitTarget struct {
	ProjectName string `yaml:"name"`
	Group       string `yaml:"group"`
	Branch      string `yaml:"branch"`
}

func NewUploader(
	ctx context.Context,
	awsAccessKey,
	awsSecretKey,
	awsRegion,
	bucket,
	glURL,
	glUsername,
	glToken,
	gqlURL,
	gqlFile,
	gqlUsername,
	gqlPassword,
	masterBundleSha,
	pubKey,
	workdir string) (*Uploader, error) {

	// indicates PR check run when different from default
	if masterBundleSha != "unused" {
		// determine if exit early can occur
		unchanged, err := detectUnchanged(ctx, gqlURL, gqlFile, gqlUsername, gqlPassword, masterBundleSha)
		if err != nil {
			return nil, err
		}
		if unchanged {
			return nil, nil
		}
	}

	cfg, err := getConfig(ctx, gqlURL, gqlFile, gqlUsername, gqlPassword)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("mkdir", "-p", workdir)
	err = cmd.Run()
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
		awsRegion:  awsRegion,
		bucket:     bucket,
		glBaseURL:  glURL,
		glUsername: glUsername,
		glToken:    glToken,
		publicKey:  pubKey,
		workdir:    workdir,
		glClient:   gl,
		s3Client:   awsS3,
		syncs:      cfg,
	}, nil
}

// Run executes steps to reconcile s3 bucket with existing state of gitlab projects
func (u *Uploader) Run(ctx context.Context, dryRun bool) error {
	log.Println("Starting run...")

	defer u.clear()

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

	err = u.cloneRepos(toUpdate)
	if err != nil {
		return err
	}

	err = u.tarRepos(toUpdate)
	if err != nil {
		return err
	}

	err = u.encryptRepoTars(toUpdate)
	if err != nil {
		return err
	}

	if dryRun {
		printDryRun(toUpdate, toDelete)
		return nil
	}

	err = u.uploadLatest(ctx, toUpdate, glCommits)
	if err != nil {
		return err
	}
	for _, update := range toUpdate {
		fmt.Println(fmt.Sprintf("s3 object for destination PID `%s/%s` successfully updated",
			update.Destination.Group,
			update.Destination.ProjectName))
	}

	err = u.removeOutdated(ctx, toDelete)
	if err != nil {
		return err
	}
	for _, delete := range toDelete {
		fmt.Println(fmt.Sprintf("s3 object with key `%s` successfully deleted", *delete))
	}

	log.Println("Run successfully completed")
	return nil
}

type DecodedKey struct {
	Group       string `json:"group"`
	ProjectName string `json:"project_name"`
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
}

// query previous graphql bundle and latest bundle then compare bundles for relevant changes
// return true if relevant attributes of both bundles are equal
// this is utilized to support early exit in PR checks
func detectUnchanged(ctx context.Context, gqlUrl, gqlFile, gqlUsername, gqlPassowrd, masterBundleSha string) (bool, error) {
	// replace `graphql` portion of path with specific sha to query
	slicedUrl := strings.Split(gqlUrl, "/")
	gqlBaseUrl := strings.Join(slicedUrl[:len(slicedUrl)-1], "/")
	gqlShaUrl := fmt.Sprintf("%s/graphqlsha/%s", gqlBaseUrl, masterBundleSha)

	// query graphql server at both prev and curr bundle
	prevCfg, err := getConfig(ctx, gqlShaUrl, gqlFile, gqlUsername, gqlPassowrd)
	if err != nil {
		return false, err
	}
	currCfg, err := getConfig(ctx, gqlUrl, gqlFile, gqlUsername, gqlPassowrd)
	if err != nil {
		return false, err
	}

	// convert cfgs to maps for comparison
	prevCfgMap := make(map[string]*SyncConfig)
	for _, sc := range prevCfg {
		prevCfgMap[fmt.Sprintf("%s/%s", sc.Source.ProjectName, sc.Source.Group)] = sc
	}
	currCfgMap := make(map[string]*SyncConfig)
	for _, sc := range currCfg {
		currCfgMap[fmt.Sprintf("%s/%s", sc.Source.ProjectName, sc.Source.Group)] = sc
	}

	return reflect.DeepEqual(prevCfgMap, currCfgMap), nil
}

// query graphql and convert result into objects for reconcile
func getConfig(ctx context.Context, gqlUrl, gqlFile, gqlUsername, gqlPassowrd string) ([]*SyncConfig, error) {
	rawCfg, err := getGraphqlRaw(ctx, gqlUrl, gqlFile, gqlUsername, gqlPassowrd)
	if err != nil {
		return nil, err
	}

	appBytes, err := yaml.Marshal(rawCfg)
	if err != nil {
		return nil, err
	}

	var config Apps
	err = yaml.Unmarshal(appBytes, &config)
	if err != nil {
		return nil, err
	}

	syncs := []*SyncConfig{}
	for _, cc := range config.CodeComponentGitSyncs {
		for _, gs := range cc.GitlabSyncs {
			if gs.GitSync != nil {
				syncs = append(syncs, gs.GitSync)
			}
		}
	}

	return syncs, nil
}

// create graphql query request and perform query with retry logic
// return is unaltered query response
func getGraphqlRaw(ctx context.Context, gqlUrl, gqlFile, gqlUsername, gqlPassowrd string) (map[string]interface{}, error) {
	client := graphql.NewClient(gqlUrl)

	query, err := ioutil.ReadFile(gqlFile)
	if err != nil {
		return nil, err
	}

	req := graphql.NewRequest(string(query))

	// default values
	if gqlUsername != "dev" && gqlPassowrd != "dev" {
		req.Header.Set("Authorization",
			fmt.Sprintf("Basic %s",
				base64.StdEncoding.EncodeToString(
					[]byte(fmt.Sprintf("%s:%s", gqlUsername, gqlPassowrd)),
				),
			),
		)
	}

	// execute query with retry logic and capture the response
	var rawCfg map[string]interface{}
	for _, backoff := range []time.Duration{1 * time.Second, 3 * time.Second, 10 * time.Second} {
		ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()

		err = client.Run(ctxTimeout, req, &rawCfg)
		if err == nil {
			break
		}

		log.Println(err)
		time.Sleep(backoff)
	}

	if err != nil {
		return nil, err
	}
	return rawCfg, nil
}

// iterates through desired Syncs (defined within config file)
// and compares latest commits on source GitLab projects against
// commits stored within s3 keys for corresponding destination GitLab projects
// return is slice of Sync that do not exist within s3Commits OR s3Commit != glCommit
// and slice of s3 object keys to delete
func (u *Uploader) getOutOfSync(ctx context.Context, glCommits pidToCommit,
	objInfos map[string]*s3ObjectInfo) ([]*SyncConfig, []*string, error) {

	outdated := []*SyncConfig{}
	toDelete := []*string{}
	for _, sync := range u.syncs {
		sourcePid := fmt.Sprintf("%s/%s", sync.Source.Group, sync.Source.ProjectName)
		destinationPid := fmt.Sprintf("%s/%s", sync.Destination.Group, sync.Destination.ProjectName)

		objInfo, exist := objInfos[destinationPid]
		if !exist {
			// new target added to config file
			outdated = append(outdated, sync)
		} else if objInfo.CommitSHA != glCommits[sourcePid] {
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

// clean target working directory
func (u *Uploader) clean(directory string) error {
	cmd := exec.Command("rm", "-rf", directory)
	cmd.Dir = u.workdir
	err := cmd.Run()
	if err != nil {
		return err
	}
	cmd = exec.Command("mkdir", directory)
	cmd.Dir = u.workdir
	err = cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

// clear all items in working directory
func (u *Uploader) clear() error {
	cmd := exec.Command("rm", "-rf", ENCRYPT_DIRECTORY, TAR_DIRECTORY, CLONE_DIRECTORY)
	cmd.Dir = u.workdir
	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
}

func printDryRun(toUpdate []*SyncConfig, toDelete []*string) {
	for _, update := range toUpdate {
		fmt.Println(fmt.Sprintf("[DRY RUN] s3 object for destination PID `%s/%s` will be updated",
			update.Destination.Group,
			update.Destination.ProjectName))
	}
	for _, delete := range toDelete {
		fmt.Println(fmt.Sprintf("[DRY RUN] s3 object with key `%s` will be deleted", *delete))
	}
	log.Println("[DRY RUN] Run successfully completed")
}
