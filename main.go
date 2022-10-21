package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/app-sre/gitlab-sync-s3-push/pkg"
)

func main() {
	var dryRun bool
	flag.BoolVar(&dryRun, "dry-run", false, "If true, will only print planned actions")
	flag.Parse()

	// define vars to look for and any defaults
	envVars, err := getEnvVars(map[string]string{
		"AWS_ACCESS_KEY_ID":     "",
		"AWS_SECRET_ACCESS_KEY": "",
		"AWS_REGION":            "",
		"AWS_S3_BUCKET":         "",
		"CONFIG_FILE_PATH":      "",
		"GITLAB_TOKEN":          "",
		"GITLAB_BASE_URL":       "",
		"PUBLIC_GPG_PATH":       "",
		"RECONCILE_SLEEP_TIME":  "5m",
	})
	if err != nil {
		log.Fatalln(err)
	}

	raw, err := os.ReadFile(envVars["CONFIG_FILE_PATH"])
	if err != nil {
		log.Fatalln(err)
	}

	uploader, err := pkg.NewUploader(
		raw,
		envVars["GITLAB_TOKEN"],
		envVars["GITLAB_BASE_URL"],
		envVars["AWS_ACCESS_KEY_ID"],
		envVars["AWS_SECRET_ACCESS_KEY"],
		envVars["AWS_REGION"],
		envVars["AWS_S3_BUCKET"],
	)
	if err != nil {
		log.Fatalln(err)
	}

	sleepDur, err := time.ParseDuration(envVars["RECONCILE_SLEEP_TIME"])
	if err != nil {
		log.Fatalln(err)
	}

	for {
		err = uploader.Run()
		if err != nil {
			log.Println(err)
		}
		time.Sleep(sleepDur)
	}
}

// iterate through keys of desired env variables and look up values
func getEnvVars(vars map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	for k := range vars {
		val := os.Getenv(k)
		if val == "" {
			// check if optional (default exists)
			if vars[k] != "" {
				result[k] = vars[k]
			} else {
				return nil, errors.New(
					fmt.Sprintf("Required environment variable missing: %s", k))
			}
		} else {
			result[k] = val
		}
	}
	return result, nil
}