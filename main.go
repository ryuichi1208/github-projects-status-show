package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"github.com/jessevdk/go-flags"
	"github.com/slack-go/slack"
	"golang.org/x/oauth2"
)

var GITHUB_TOKEN string
var SLACK_TOKEN string
var SLACK_CHANNEL string
var opts options

type options struct {
	Organization string `short:"o" long:"organization" description:"mysql user" default:"root" required:"false"`
	Repository   string `short:"r" long:"repository" description:"mysql host" default:"localhost" required:"true"`
	BaseUrl      string `short:"b" long:"base-url" description:"mysql port" default:"3306" required:"false"`
}

type GitHub struct {
	client *github.Client
	org    string
	repo   string
	ctx    context.Context
}

type Slack struct {
	webhook url.URL
}

func NewGitHub(baseURL, org, repo string, ctx context.Context) (GitHub, error) {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: GITHUB_TOKEN},
	)
	tc := oauth2.NewClient(ctx, ts)

	client, err := github.NewEnterpriseClient(baseURL, "", tc)
	if err != nil {
		fmt.Println(err)
	}

	return GitHub{
		client: client,
		org:    org,
		repo:   repo,
		ctx:    ctx,
	}, nil
}

func (g GitHub) ListProjects() (int64, error) {
	projectsObj, _, err := g.client.Repositories.ListProjects(g.ctx, g.org, g.repo, nil)
	if err != nil {
		fmt.Println(err)
	}
	for _, project := range projectsObj {
			return *project.ID, nil
		}
	}

	return -1, fmt.Errorf("no such projects")

}

func (g GitHub) ListProjectsColumn(id int64) ([]*github.ProjectColumn, error) {
	columns, resp, err := g.client.Projects.ListProjectColumns(g.ctx, id, nil)
	if err != nil {
		return nil, err
	}
	if resp.Status != "200 OK" {
		return nil, fmt.Errorf("not 200")
	}

	return columns, nil
}

func (g GitHub) ListProjectCards(id int64) ([]*github.ProjectCard, error) {
	lst := github.ListOptions{
		PerPage: 200,
	}
	opt := &github.ProjectCardListOptions{ListOptions: lst}
	cards, resp, err := g.client.Projects.ListProjectCards(g.ctx, id, opt)
	if err != nil {
		return nil, err
	}
	if resp.Status != "200 OK" {
		return nil, fmt.Errorf("not 200")
	}

	return cards, nil
}

func (g GitHub) GetIssue(u *url.URL) (string, error) {
	var org, repos, num string
	tmps := strings.Split(u.Path, "/")
	if strings.Contains(u.Path, "/api/v3/repos/") {
		org, repos, num = tmps[4], tmps[5], tmps[7]

	} else {
		org, repos, num = tmps[1], tmps[2], tmps[4]
	}

	i, err := strconv.Atoi(num)
	if err != nil {
		return "", err
	}

	issue, resp, err := g.client.Issues.Get(g.ctx, org, repos, i)
	if err != nil {
		return "", err
	}
	if resp.Status != "200 OK" {
		return "", fmt.Errorf("not 200")
	}

	for _, user := range issue.Assignees {
		return *user.Login, nil
	}

	return "", nil

}

func parseArgs(args []string) error {
	_, err := flags.ParseArgs(&opts, os.Args)

	if err != nil {
		return err
	}

	return nil
}

func extractIssueURL(note string) (*url.URL, error) {
	var u *url.URL
	var err error

	for _, text := range strings.Split(note, "\n") {
		if strings.Contains(text, "https") && strings.Contains(text, "issue") {
			u, err = url.Parse(text)
			if err != nil {
				return u, err
			}
			return u, nil
		}
	}
	return nil, err
}

func Do() error {
	ctx := context.Background()
	github, err := NewGitHub(opts.BaseUrl, opts.Organization, opts.Repository, ctx)
	if err != nil {
		return err
	}

	id, err := github.ListProjects()
	if err != nil {
		return err
	}

	columns, err := github.ListProjectsColumn(id)
	if err != nil {
		return err
	}

	var u *url.URL
	issueCnt := make(map[string]int64)
	assigneCnt := make(map[string]int64)

	for _, columns := range columns {
		cards, err := github.ListProjectCards(*columns.ID)
		if err != nil {
			return err
		}
		issueCnt[*columns.Name] = int64(len(cards))

		if *columns.Name == "In progress" {
			for _, card := range cards {
				if card.Note != nil {
					// maybe note
					u, _ = extractIssueURL(*card.Note)
				} else {
					// maybe issue
					u, _ = extractIssueURL(card.GetContentURL())
				}

				if u != nil {
					user, err := github.GetIssue(u)
					if err != nil {
						fmt.Println(err)
					}
					if user == "" {
						user = "nil"
					}
					assigneCnt[user]++
				}
			}
		}
	}

	s := Slack{}
	s.sendMsg(issueCnt, assigneCnt)
	return nil
}

type SlackData struct {
	Blocks []struct {
		Type string `json:"type"`
		Text struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Emoji bool   `json:"emoji"`
		} `json:"text,omitempty"`
		Fields []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"fields,omitempty"`
	} `json:"blocks"`
}

func (s Slack) sendMsg(statusCnt, assigneCnt map[string]int64) {
	var iss []*slack.TextBlockObject
	var is *slack.TextBlockObject
	for k, v := range statusCnt {
		is = &slack.TextBlockObject{
			Type: "mrkdwn",
			Text: fmt.Sprintf("*%s*\n%d", k, v),
		}
		iss = append(iss, is)
	}

	var as *slack.TextBlockObject
	var ass []*slack.TextBlockObject
	for k, v := range assigneCnt {
		as = &slack.TextBlockObject{
			Type: "mrkdwn",
			Text: fmt.Sprintf("*%s*\n%d", k, v),
		}
		ass = append(ass, as)
	}
	c := slack.New(SLACK_TOKEN)
	_, ts, err := c.PostMessage(SLACK_CHANNEL, slack.MsgOptionBlocks(
		slack.NewSectionBlock(
			iss,
			slack.NewAccessory(
				slack.NewImageBlockElement("https://name-power.net/ti.php?text=%E6%97%A5%E7%9B%B4", "alt text for image"),
			),
		),
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			ass,
			nil,
		),
	))
	_, _, err = c.PostMessage(SLACK_CHANNEL, slack.MsgOptionText("test", true), slack.MsgOptionTS(ts))
	if err != nil {
		panic(err)
	}
}

func init() {
	GITHUB_TOKEN = os.Getenv("GITHUB_TOKEN")
	if GITHUB_TOKEN == "" {
		fmt.Println("Not set GITHUB_TOKEN")
		os.Exit(1)
	}
	SLACK_TOKEN = os.Getenv("SLACK_TOKEN")
	if GITHUB_TOKEN == "" {
		fmt.Println("Not set SLACK_TOKEN")
		os.Exit(1)
	}
	SLACK_CHANNEL = os.Getenv("SLACK_CHANNEL")
	if GITHUB_TOKEN == "" {
		fmt.Println("Not set SLACK_CHANNEL")
		os.Exit(1)
	}
}

func main() {
	err := parseArgs(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}

	err = Do()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
