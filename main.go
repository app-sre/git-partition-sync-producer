package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/app-sre/git-partition-sync-producer/pkg"
	"github.com/app-sre/git-partition-sync-producer/pkg/utils"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	var dryRun bool
	var runOnce bool
	var prCheck bool
	flag.BoolVar(&dryRun, "dry-run", true, "If true, will only print planned actions")
	flag.BoolVar(&runOnce, "run-once", true, "If true, will exit after single execution")
	flag.BoolVar(&prCheck, "pr-check", false, "If true, will compare graphql bundles for early exit")
	flag.Parse()

	// define vars to look for and any defaults
	envVars, err := getEnvVars(map[string]string{
		"AWS_ACCESS_KEY_ID":     "",
		"AWS_SECRET_ACCESS_KEY": "",
		"AWS_REGION":            "",
		"AWS_S3_BUCKET":         "",
		"GITLAB_BASE_URL":       "",
		"GITLAB_USERNAME":       "",
		"GITLAB_TOKEN":          "",
		"GRAPHQL_SERVER":        "",
		"GRAPHQL_QUERY_FILE":    "/query.graphql",
		"GRAPHQL_USERNAME":      "dev",
		"GRAPHQL_PASSWORD":      "dev",
		"INSTANCE_SHARD":        "fedramp",
		"METRICS_SERVER_PORT":   "9090",
		"MASTER_BUNDLE_SHA256":  "unused",
		"PUBLIC_KEY":            "",
		"RECONCILE_SLEEP_TIME":  "5m",
		"WORKDIR":               "/working",
	})
	if err != nil {
		log.Fatalln(err)
	}

	var sleepDur time.Duration
	if !runOnce {
		sleepDur, err = time.ParseDuration(envVars["RECONCILE_SLEEP_TIME"])
		if err != nil {
			log.Fatalln(err)
		}

		// configure prometheus metrics handler
		http.Handle("/metrics", promhttp.Handler())
		go func() {
			http.ListenAndServe(fmt.Sprintf(":%s", envVars["METRICS_SERVER_PORT"]), nil)
		}()
	}

	// processed separately from env map above because optional w/ no default val
	masterBundleSha := os.Getenv("MASTER_BUNDLE_SHA256")
	if prCheck && len(masterBundleSha) == 0 {
		log.Fatalln("`MASTER_BUNDLE_SHA256` must be set when pr-check flag is true.")
	}
	envVars["MASTER_BUNDLE_SHA256"] = masterBundleSha

	// retrieve raw from graphql

	for {
		status := 0
		start := time.Now()

		ctx := context.Background()
		uploader, err := pkg.NewUploader(
			ctx,
			envVars["AWS_ACCESS_KEY_ID"],
			envVars["AWS_SECRET_ACCESS_KEY"],
			envVars["AWS_REGION"],
			envVars["AWS_S3_BUCKET"],
			envVars["GITLAB_BASE_URL"],
			envVars["GITLAB_USERNAME"],
			envVars["GITLAB_TOKEN"],
			envVars["GRAPHQL_SERVER"],
			envVars["GRAPHQL_QUERY_FILE"],
			envVars["GRAPHQL_USERNAME"],
			envVars["GRAPHQL_PASSWORD"],
			envVars["MASTER_BUNDLE_SHA256"],
			envVars["PUBLIC_KEY"],
			envVars["WORKDIR"],
		)
		if err != nil {
			log.Fatalln(err)
		} else if uploader == nil { // return only nil, nil on exit early
			log.Println("Relevant attributes have not changed. Exiting early.")
			os.Exit(0)
		}

		err = uploader.Run(ctx, dryRun)
		if err != nil {
			log.Println(err)
			status = 1
		}

		if runOnce {
			os.Exit(0)
		} else {
			utils.RecordMetrics(envVars["INSTANCE_SHARD"], status, time.Since(start))
			time.Sleep(sleepDur)
		}
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
