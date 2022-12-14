package utils

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/app-sre/git-partition-sync-producer/pkg"
	"gopkg.in/yaml.v3"
)

type SaasFile struct {
	SaasDefinitions []Saas `yaml:"saas_files"`
}

type Saas struct {
	Name              string             `yaml:"name"`
	ResourceTemplates []ResourceTemplate `yaml:"resourceTemplates"`
}

type ResourceTemplate struct {
	Targets []Target `yaml:"targets"`
}

type Target struct {
	Ref string `yaml:"ref"`
}

// query previous graphql bundle and latest bundle then compare bundles for relevant changes
// return true if gitlabSyncs and promotion targets of git partition sync for both bundles are same
func EarlyExit(ctx context.Context,
	gqlUrl,
	gqlFile,
	gqlUsername,
	gqlPassowrd,
	prevBundleSha,
	saasName string) (bool, error) {

	// replace `graphql` portion of path with specific sha to query
	slicedUrl := strings.Split(gqlUrl, "/")
	gqlBaseUrl := strings.Join(slicedUrl[:len(slicedUrl)-1], "/")
	gqlShaUrl := fmt.Sprintf("%s/graphqlsha/%s", gqlBaseUrl, prevBundleSha)

	// query graphql server at both prev and curr bundle
	prevGqlRaw, err := pkg.GetGraphqlRaw(ctx, gqlShaUrl, gqlFile, gqlUsername, gqlPassowrd)
	if err != nil {
		return false, err
	}
	currGqlRaw, err := pkg.GetGraphqlRaw(ctx, gqlUrl, gqlFile, gqlUsername, gqlPassowrd)
	if err != nil {
		return false, err
	}

	syncIsSame, err := isSyncSame(prevGqlRaw, currGqlRaw)
	if err != nil {
		return false, err
	}
	if !syncIsSame {
		return false, nil
	}

	saasIsSame, err := isSaasSame(prevGqlRaw, currGqlRaw, saasName)
	if err != nil {
		return false, err
	}
	if !saasIsSame {
		return false, nil
	}

	return true, nil
}

// detects change to any GitlabSync definitions between both bundles
func isSyncSame(prevGqlRaw, currGqlRaw map[string]interface{}) (bool, error) {
	prevBytes, err := yaml.Marshal(map[string]interface{}{
		"apps_v1": prevGqlRaw["apps_v1"],
	})
	currBytes, err := yaml.Marshal(map[string]interface{}{
		"apps_v1": currGqlRaw["apps_v1"],
	})

	var prevSyncCfg, currSyncCfg pkg.Apps
	err = yaml.Unmarshal(prevBytes, &prevSyncCfg)
	if err != nil {
		return false, err
	}
	err = yaml.Unmarshal(currBytes, &currSyncCfg)
	if err != nil {
		return false, err
	}

	prevCfgMap := make(map[string]*pkg.SyncConfig)
	for _, cc := range prevSyncCfg.CodeComponentGitSyncs {
		for _, gs := range cc.GitlabSyncs {
			if gs.GitSync != nil {
				prevCfgMap[fmt.Sprintf("%s/%s", gs.GitSync.Source.ProjectName, gs.GitSync.Source.Group)] = gs.GitSync
			}
		}
	}
	currCfgMap := make(map[string]*pkg.SyncConfig)
	for _, cc := range currSyncCfg.CodeComponentGitSyncs {
		for _, gs := range cc.GitlabSyncs {
			if gs.GitSync != nil {
				currCfgMap[fmt.Sprintf("%s/%s", gs.GitSync.Source.ProjectName, gs.GitSync.Source.Group)] = gs.GitSync
			}
		}
	}

	return reflect.DeepEqual(prevCfgMap, currCfgMap), nil
}

// detects change to saas file for git-partition-sync-producer
// required to avoid early exit on promotions of this app
func isSaasSame(prevGqlRaw, currGqlRaw map[string]interface{}, saasName string) (bool, error) {
	prevBytes, err := yaml.Marshal(map[string]interface{}{
		"saas_files": prevGqlRaw["saas_files"],
	})
	currBytes, err := yaml.Marshal(map[string]interface{}{
		"saas_files": currGqlRaw["saas_files"],
	})

	var prevSaasCfg, currSaasCfg SaasFile
	err = yaml.Unmarshal(prevBytes, &prevSaasCfg)
	if err != nil {
		return false, err
	}
	err = yaml.Unmarshal(currBytes, &currSaasCfg)
	if err != nil {

		return false, err
	}

	var prevRTs []ResourceTemplate
	for _, s := range prevSaasCfg.SaasDefinitions {
		if s.Name == saasName {
			prevRTs = s.ResourceTemplates
		}
	}
	var currRTs []ResourceTemplate
	for _, s := range currSaasCfg.SaasDefinitions {
		if s.Name == saasName {
			currRTs = s.ResourceTemplates
		}
	}

	return reflect.DeepEqual(prevRTs, currRTs), nil
}
