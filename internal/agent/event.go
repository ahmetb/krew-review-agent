package agent

import (
	"github.com/ahmetb/krew-review-agent/internal/githubclient"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

// ReviewContextFromEvent maps a parsed GitHub pull_request webhook event into
// the ReviewContext used by the agent and its tools.
func ReviewContextFromEvent(e githubclient.PREvent) tools.ReviewContext {
	return tools.ReviewContext{
		Owner:    e.Repository.Owner.Login,
		Repo:     e.Repository.Name,
		PRNumber: e.Number,
		Title:    e.PullRequest.Title,
		Body:     e.PullRequest.Body,
		Author:   e.PullRequest.User.Login,
		HeadSHA:  e.PullRequest.Head.SHA,
		Action:   e.Action,
	}
}
