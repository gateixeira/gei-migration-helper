package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofri/go-github-ratelimit/github_ratelimit"
	"github.com/google/go-github/v59/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

type Repository *github.Repository

type Workflow *github.Workflow

type ScanningAnalysis *github.ScanningAnalysis

type Issue *github.Issue

type BranchProtectionRule struct {
	Nodes []struct {
		Id string
	}
	PageInfo struct {
		EndCursor   githubv4.String
		HasNextPage bool
	}
}

var (
	ctx         context.Context
	clientV3    *github.Client
	clientV4    *githubv4.Client
	accessToken string
)

var (
	ErrBranchProtectionDeletion = errors.New("error deleting branch protection rules")
	ErrRepositoryNotFound       = errors.New("repository not found")
	ErrIssueNotFound            = errors.New("issue not found")
)

func checkClients(token string) error {
	if clientV3 == nil || clientV4 == nil || token != accessToken {
		accessToken = token
		ctx = context.Background()

		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc := oauth2.NewClient(ctx, ts)
		rateLimiter, err := github_ratelimit.NewRateLimitWaiterClient(tc.Transport)

		if err != nil {
			return err
		}

		clientV3 = github.NewClient(rateLimiter)
		clientV4 = githubv4.NewClient(rateLimiter)
	}

	return nil
}

func DeleteBranchProtections(organization string, repository string, token string) error {
	checkClients(token)

	var query struct {
		Repository struct {
			BranchProtectionRules BranchProtectionRule `graphql:"branchProtectionRules(first: 100, after: $cursor)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	variables := map[string]interface{}{
		"owner":  githubv4.String(organization),
		"name":   githubv4.String(repository),
		"cursor": (*githubv4.String)(nil),
	}

	results := make([]string, 0)
	for {
		err := clientV4.Query(ctx, &query, variables)
		if err != nil {
			return err
		}
		for _, protection := range query.Repository.BranchProtectionRules.Nodes {
			results = append(results, protection.Id)
		}

		variables["cursor"] = query.Repository.BranchProtectionRules.PageInfo.EndCursor

		if !query.Repository.BranchProtectionRules.PageInfo.HasNextPage {
			break
		}
	}

	// // delete all branch protections
	for _, branchProtection := range results {
		var mutate struct {
			DeleteBranchProtectionRule struct { // Empty struct does not work
				ClientMutationId githubv4.ID
			} `graphql:"deleteBranchProtectionRule(input: $input)"`
		}
		input := githubv4.DeleteBranchProtectionRuleInput{
			BranchProtectionRuleID: branchProtection,
		}

		ctx := context.WithValue(context.Background(), ctx, branchProtection)
		err := clientV4.Mutate(ctx, &mutate, input, nil)

		if err != nil {
			return ErrBranchProtectionDeletion
		}
	}

	return nil
}

func ChangeGHASOrgSettings(organization string, activate bool, token string) error {
	checkClients(token)

	//create new organization object
	newOrgSettings := github.Organization{
		AdvancedSecurityEnabledForNewRepos:             &activate,
		SecretScanningPushProtectionEnabledForNewRepos: &activate,
		SecretScanningEnabledForNewRepos:               &activate,
	}

	// Update the organization
	_, _, err := clientV3.Organizations.Edit(ctx, organization, &newOrgSettings)

	return err
}

func ChangeGhasRepoSettings(organization string, repository Repository, ghas string, secretScanning string, pushProtection string, token string) error {
	checkClients(token)

	var payload *github.SecurityAndAnalysis
	//GHAS is always enabled for public repositories and PATCH fails when trying to set to disabled
	if *repository.Visibility == "public" {
		payload = &github.SecurityAndAnalysis{
			SecretScanning: &github.SecretScanning{
				Status: &secretScanning,
			},
			SecretScanningPushProtection: &github.SecretScanningPushProtection{
				Status: &pushProtection,
			},
		}
	} else {
		payload = &github.SecurityAndAnalysis{
			AdvancedSecurity: &github.AdvancedSecurity{
				Status: &ghas,
			},
			SecretScanning: &github.SecretScanning{
				Status: &secretScanning,
			},
			SecretScanningPushProtection: &github.SecretScanningPushProtection{
				Status: &pushProtection,
			},
		}
	}

	//create new repository object
	newRepoSettings := github.Repository{
		SecurityAndAnalysis: payload,
	}

	// Update the repository
	_, response, err := clientV3.Repositories.Edit(ctx, organization, *repository.Name, &newRepoSettings)

	slog.Debug("waiting 10 seconds for changes to apply...")
	time.Sleep(10 * time.Second)

	if err != nil {
		if response.StatusCode == 422 {
			// Skip if error is 422 as this is likely a false negative
			return nil
		}
	}

	return err
}

func GetRepository(repoName string, org string, token string) (Repository, error) {
	checkClients(token)

	repo, _, err := clientV3.Repositories.Get(ctx, org, repoName)
	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 404 {
				return nil, ErrRepositoryNotFound
			}
		}

		return nil, err
	}

	return repo, nil
}

func GetRepositories(org string, token string) ([]Repository, error) {
	checkClients(token)

	// list all repositories for the organization
	opt := &github.RepositoryListByOrgOptions{Type: "all", ListOptions: github.ListOptions{PerPage: 10}}
	var allRepos []*github.Repository
	for {
		repos, response, err := clientV3.Repositories.ListByOrg(ctx, org, opt)

		if err != nil {
			return nil, err
		}
		allRepos = append(allRepos, repos...)
		if response.NextPage == 0 {
			break
		}
		opt.Page = response.NextPage
	}

	var allReposStruct []Repository
	for _, repo := range allRepos {
		allReposStruct = append(allReposStruct, repo)
	}

	return allReposStruct, nil
}

func ChangeRepositoryVisibility(organization string, repository string, visibility string, token string) error {
	checkClients(token)

	//create new repository object
	newRepoSettings := github.Repository{
		Visibility: &visibility,
	}

	// Update the repository
	_, _, err := clientV3.Repositories.Edit(ctx, organization, repository, &newRepoSettings)

	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 422 {
				// Skip if error is 422 as this is likely a false negative
				return nil
			}
		}
	}

	return err
}

func GetAllActiveWorkflowsForRepository(organization string, repository string, token string) ([]Workflow, error) {
	checkClients(token)

	// list all workflows for the repository
	opt := &github.ListOptions{PerPage: 10}
	var allWorkflows []*github.Workflow
	for {
		workflows, response, err := clientV3.Actions.ListWorkflows(ctx, organization, repository, opt)

		if err != nil {
			return nil, err
		}
		allWorkflows = append(allWorkflows, workflows.Workflows...)
		if response.NextPage == 0 {
			break
		}
		opt.Page = response.NextPage
	}

	var activeWorkflowsStruct []Workflow
	for _, workflow := range allWorkflows {
		if *workflow.State == "active" {
			activeWorkflowsStruct = append(activeWorkflowsStruct, workflow)
		}
	}

	return activeWorkflowsStruct, nil
}

func GetAllWorkflowsForRepository(organization string, repository string, token string) ([]Workflow, error) {
	checkClients(token)

	// list all workflows for the repository
	opt := &github.ListOptions{PerPage: 10}
	var allWorkflows []Workflow
	for {
		workflows, response, err := clientV3.Actions.ListWorkflows(ctx, organization, repository, opt)

		if err != nil {
			return nil, err
		}

		for _, workflow := range workflows.Workflows {
			// add all workflows to the list
			allWorkflows = append(allWorkflows, workflow)
		}

		if response.NextPage == 0 {
			break
		}
		opt.Page = response.NextPage
	}

	return allWorkflows, nil
}

func DisableWorkflowsForRepository(organization string, repository string, workflows []Workflow, token string) error {
	checkClients(token)

	// disable all workflows
	for _, workflow := range workflows {
		_, err := clientV3.Actions.DisableWorkflowByID(ctx, organization, repository, *workflow.ID)

		if _, ok := err.(*github.ErrorResponse); ok {
			slog.Debug(fmt.Sprint("failed to disable workflow: ", workflow.Name, " - will not stop migration"))
			return nil
		}
	}

	return nil
}

func EnableWorkflowsForRepository(organization string, repository string, workflows []Workflow, token string) error {
	checkClients(token)

	// enable all workflows
	for _, workflow := range workflows {
		_, err := clientV3.Actions.EnableWorkflowByID(ctx, organization, repository, *workflow.ID)

		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 422 {
				// Skip if error is 422 as this is likely a false negative
				return nil
			}
			return err
		}
	}

	return nil
}

func GetCodeScanningAnalysis(organization string, repository string, defaultBranch string, token string) ([]ScanningAnalysis, error) {
	checkClients(token)

	analysis, _, err := clientV3.CodeScanning.ListAnalysesForRepo(ctx, organization, repository, &github.AnalysesListOptions{Ref: &defaultBranch})

	if err != nil {
		//test if error code is 404
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 404 {
				return nil, nil
			} else {
				return nil, err
			}
		}
	}

	if len(analysis) == 0 {
		return nil, nil
	}

	convertedAnalysis := make([]ScanningAnalysis, len(analysis))
	for i, a := range analysis {
		convertedAnalysis[i] = a
	}

	return convertedAnalysis, nil
}

func ArchiveRepository(organization string, repository string, token string) error {
	checkClients(token)

	return ChangeArchiveRepository(organization, repository, true, token)
}

func UnarchiveRepository(organization string, repository string, token string) error {
	checkClients(token)

	return ChangeArchiveRepository(organization, repository, false, token)
}

func ChangeArchiveRepository(organization string, repository string, archive bool, token string) error {
	newRepoSettings := github.Repository{
		Archived: &archive,
	}

	_, _, err := clientV3.Repositories.Edit(ctx, organization, repository, &newRepoSettings)

	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 403 {
				//repository is already archived
				return nil
			}
		}
	}

	return err
}

func CreateRepository(organization string, repository string, token string) error {
	checkClients(token)

	newRepo := &github.Repository{
		Name: &repository,
	}

	_, _, err := clientV3.Repositories.Create(ctx, organization, newRepo)

	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 422 {
				//repository already exists
				return nil
			}
		}
	}

	return err
}

func CreateIssue(organization string, repository string, title string, body string, token string) error {
	checkClients(token)

	newIssue := &github.IssueRequest{
		Title: &title,
		Body:  &body,
	}

	_, _, err := clientV3.Issues.Create(ctx, organization, repository, newIssue)

	return err
}

func GetIssue(organization string, repository string, issueNumber int, token string) (Issue, error) {
	checkClients(token)

	issue, _, err := clientV3.Issues.Get(ctx, organization, repository, issueNumber)

	if err != nil {
		if err, ok := err.(*github.ErrorResponse); ok {
			if err.Response.StatusCode == 404 {
				return nil, ErrIssueNotFound
			}
		}

		return nil, err
	}

	return issue, nil
}