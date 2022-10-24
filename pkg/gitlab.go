package pkg

import "fmt"

// keys are gitlab PID (gitlab_group/project_name)
// values are commit SHA
type pidToCommit map[string]string

// returns a map of project PIDs (gitlab_group/project_name) to latest commit on specified source branch
func (u *Uploader) getLatestGitlabCommits() (pidToCommit, error) {
	latestCommits := make(pidToCommit)
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

func cloneRepositories(baseURL string) error {

	return nil
}
