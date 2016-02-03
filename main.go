package main

import (
	"bufio"
	"bytes"
	"container/list"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/walter-cd/walter-server/api"
	"github.com/walter-cd/walter/config"
	"github.com/walter-cd/walter/log"
	"github.com/walter-cd/walter/services"
	"github.com/walter-cd/walter/stages"
	"github.com/walter-cd/walter/walter"
)

var server string
var maxWorkers int64
var workers int64 = 0
var interval int64
var workingDir string
var baseUrl string

func main() {
	//flags := flag.NewFlagSet("walter-agent", flag.ExitOnError)
	flag.StringVar(&server, "server", "http://localhost:8080/", "URL of walter-server")
	flag.Int64Var(&maxWorkers, "max_workers", 5, "Maximum number of walter workers")
	flag.Int64Var(&interval, "interval", 1, "Job polling interval by seconds")
	flag.StringVar(&workingDir, "working_dir", "/var/lib/walter/workspace", "Working directory")
	flag.StringVar(&baseUrl, "base_url", "", "Base URL of walter-server to access from web browsers")

	flag.Parse()

	log.Info("walter-agent started")

	queue := make(chan api.Job)
	done := make(chan bool)
	go pollJob(queue)
	go processJob(queue)
	<-done
}

func pollJob(queue chan api.Job) {
	for {
		time.Sleep(time.Duration(interval) * time.Second)

		if workers >= maxWorkers {
			continue
		}

		res, err := http.Get(fmt.Sprintf("%s/api/v1/jobs/pop", server))

		if err != nil {
			log.Error(err.Error())
			time.Sleep(5 * time.Second)
			continue
		}

		if res.Status == "200 OK" {
			rb := bufio.NewReader(res.Body)
			var body string
			for {
				s, err := rb.ReadString('\n')
				body = body + s
				if err == io.EOF {
					break
				}
			}

			var job api.Job
			err := json.Unmarshal([]byte(body), &job)
			if err != nil {
				log.Error(err.Error())
			}
			queue <- job
		}
	}
}

func processJob(queue chan api.Job) {
	done := make(chan bool)

	for {
		select {
		case job := <-queue:
			go runWalter(job, done, workers)
			workers++
		case <-done:
			workers--
		}
	}
}

func runWalter(job api.Job, done chan bool, num int64) {
	workerDir := workingDir + "/" + strconv.FormatInt(num, 10)
	if err := os.MkdirAll(workerDir, 0755); err != nil {
		panic(err)
	}

	repoDir := workerDir + "/" + job.Project
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		panic(err)
	}

	out, err := exec.Command("git", "clone", job.CloneUrl, repoDir).CombinedOutput()
	log.Debug((string(out)))
	if err != nil {
		log.Debug(err.Error())
	}

	os.Chdir(repoDir)

	var ref string
	if job.PullRequestNumber != 0 {
		ref = fmt.Sprintf("+refs/pull/%d/head", job.PullRequestNumber)
	}

	out, err = exec.Command("git", "fetch", "origin", ref).CombinedOutput()
	log.Debug((string(out)))
	if err != nil {
		log.Debug(err.Error())
	}

	out, err = exec.Command("git", "checkout", job.Revision).CombinedOutput()
	log.Debug((string(out)))
	if err != nil {
		log.Debug(err.Error())
	}

	opts := &config.Opts{
		PipelineFilePath: "./pipeline.yml",
		Mode:             "local",
	}

	w, _ := walter.New(opts)
	start := time.Now().Unix()
	result := w.Run()
	end := time.Now().Unix()

	reportId := postReport(job, result, w, start, end)

	updateStatus(job, result, w, reportId)
	notify(job, result, w, reportId)

	done <- true
}

func postReport(job api.Job, result bool, w *walter.Walter, start int64, end int64) int64 {
	var status string
	if result {
		status = "Passed"
	} else {
		status = "Failed"
	}

	report := &api.Report{
		Project: &api.Project{
			Name: job.Project,
			Repo: job.HtmlUrl,
		},
		Status:     status,
		Branch:     job.Branch,
		CompareUrl: job.CompareUrl,
		TriggeredBy: api.User{
			Name:      job.TriggeredBy.Name,
			Url:       job.TriggeredBy.Url,
			AvatarUrl: job.TriggeredBy.AvatarUrl,
		},
		Start: start,
		End:   end,
	}

	for _, commit := range job.Commits {
		report.Commits = append(report.Commits, &api.Commit{
			Revision: commit.Revision,
			Author:   commit.Author,
			Message:  commit.Message,
			Url:      commit.Url,
		})
	}

	for s := w.Engine.Resources.Pipeline.Stages.Front(); s != nil; s = s.Next() {
		stage := s.Value.(*stages.CommandStage)

		var status string
		if stage.GetReturnValue() {
			status = "Passed"
		} else {
			status = "Failed"
		}

		report.Stages = append(report.Stages, &api.Stage{
			Name:   stage.StageName,
			Status: status,
			Stages: getChildStages(stage.ChildStages),
			Log:    stage.CombinedResult,
			Start:  stage.Start,
			End:    stage.End,
		})
	}

	b, _ := json.Marshal(report)

	client := &http.Client{}

	u, _ := url.Parse(server)
	u.Path = "/api/v1/reports"
	req, err := http.NewRequest("POST", u.String(), bytes.NewBuffer(b))

	if err != nil {
		log.Error(err.Error())
	}

	req.Header.Add("Content-Type", "application/json")

	res, _ := client.Do(req)

	rb := bufio.NewReader(res.Body)
	var body string
	for {
		s, err := rb.ReadString('\n')
		body = body + s
		if err == io.EOF {
			break
		}
	}

	var data api.Report
	err = json.Unmarshal([]byte(body), &data)
	if err != nil {
		log.Error(err.Error())
	}

	return data.Id
}

func getChildStages(l list.List) (st []*api.Stage) {
	for s := l.Front(); s != nil; s = s.Next() {

		stage := s.Value.(*stages.CommandStage)

		var status string
		if stage.GetReturnValue() {
			status = "Passed"
		} else {
			status = "Failed"
		}

		st = append(st, &api.Stage{
			Name:   stage.StageName,
			Status: status,
			Stages: getChildStages(stage.ChildStages),
			Log:    stage.CombinedResult,
			Start:  stage.Start,
			End:    stage.End,
		})
	}

	return
}

func updateStatus(job api.Job, result bool, w *walter.Walter, reportId int64) {
	github := w.Engine.Resources.RepoService

	project := strings.Split(job.Project, "/")

	github.(*services.GitHubClient).From = project[0]
	github.(*services.GitHubClient).Repo = project[1]

	baseUrl, _ := url.Parse(job.StatusesUrl)
	baseUrl.Path = ""

	github.(*services.GitHubClient).BaseUrl = baseUrl

	state := ""
	message := ""
	if result {
		state = "success"
		message = "Walter build succeeded"
	} else {
		state = "fail"
		message = "Walter build failed"
	}

	res := services.Result{
		State:   state,
		Message: message,
		SHA:     job.Revision,
		Url:     buildUrl(job, reportId),
	}

	github.RegisterResult(res)
}

func notify(job api.Job, result bool, w *walter.Walter, reportId int64) {
	reporter := w.Engine.Resources.Reporter
	var status string
	var color string

	if result {
		status = "SUCCESS"
		color = "good" // FIXME: This color name is for Slack.
	} else {
		status = "FAILURE"
		color = "danger" // FIXME: This color name is for Slack.
	}

	reporter.Post(fmt.Sprintf("%s - #%d %s (<%s|Open>)", job.Project, reportId, status, buildUrl(job, reportId)), color)
}

func buildUrl(job api.Job, reportId int64) string {
	if baseUrl == "" {
		baseUrl = server
	}
	u, _ := url.Parse(baseUrl)
	values := u.Query()
	values.Add("project", job.Project)
	values.Add("report", strconv.FormatInt(reportId, 10))
	u.RawQuery = values.Encode()

	return u.String()
}
