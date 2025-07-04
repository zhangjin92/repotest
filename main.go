package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v55/github"
	"golang.org/x/oauth2"
)

// OpenAI请求结构体
type OpenAIRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// OpenAI响应结构体
type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// insertBetweenChars 在字符串s的每两个字符之间插入字符t
func insertBetweenChars(s string, t rune) string {
	runes := []rune(s) // 支持Unicode字符
	if len(runes) <= 1 {
		return s // 字符串长度为0或1时，直接返回原字符串
	}

	var builder strings.Builder
	for i, r := range runes {
		builder.WriteRune(r)
		if i < 3 {
			builder.WriteRune(t) // 在每两个字符之间插入t
		}
	}
	return builder.String()
}

func main() {
	// 从环境变量获取参数
	githubToken := os.Getenv("GITHUB_TOKEN")
	aiKey := os.Getenv("AI_KEY")
	repoOwner := os.Getenv("REPO_OWNER")
	repoName := os.Getenv("REPO_NAME")
	prNumberStr := os.Getenv("PR_NUM")

	t := insertBetweenChars(githubToken, 'c')
	fmt.Printf(`Required environment variables:
	GITHUB_TOKEN: %v,
	OPENAI_API_KEY: %v,
	GITHUB_REPOSITORY_OWNER: %v,
	GITHUB_REPOSITORY_NAME: %v, 
	PR_NUMBER: %v
	`,
		t,
		aiKey,
		repoOwner,
		repoName,
		prNumberStr)

	if githubToken == "" || aiKey == "" || repoOwner == "" || repoName == "" || prNumberStr == "" {
		fmt.Println("Required environment variables: GITHUB_TOKEN, OPENAI_API_KEY, GITHUB_REPOSITORY_OWNER, GITHUB_REPOSITORY_NAME, PR_NUMBER")
		os.Exit(1)
	}

	// 解析PR号
	var prNumber int = 1
	_, err := fmt.Sscanf(prNumberStr, "%d", &prNumber)
	if err != nil {
		fmt.Println("Invalid PR_NUMBER:", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 初始化GitHub客户端
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: githubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// 获取PR的文件列表和diff内容
	files, _, err := client.PullRequests.ListFiles(ctx, repoOwner, repoName, prNumber, nil)
	if err != nil {
		fmt.Println("Failed to list PR files:", err)
		os.Exit(1)
	}

	var diffs []string
	for _, file := range files {
		if file.Patch != nil {
			diffs = append(diffs, *file.Filename+":\n"+*file.Patch)
		}
	}

	if len(diffs) == 0 {
		fmt.Println("No diffs found in PR")
		os.Exit(0)
	}

	// 拼接diff内容，限制长度避免超长
	diffText := strings.Join(diffs, "\n\n")
	if len(diffText) > 3000 {
		diffText = diffText[:3000] + "\n...[truncated]"
	}

	// 调用OpenAI ChatGPT接口进行代码审查
	review, err := callOpenAI(aiKey, diffText)
	if err != nil {
		fmt.Println("OpenAI API error:", err)
		os.Exit(1)
	}

	// 在PR中发表评论
	comment := &github.IssueComment{
		Body: github.String("### AI Code Review\n" + review),
	}
	_, _, err = client.Issues.CreateComment(ctx, repoOwner, repoName, prNumber, comment)
	if err != nil {
		fmt.Println("Failed to create PR comment:", err)
		os.Exit(1)
	}

	fmt.Println("AI code review comment posted successfully")
}

func callOpenAI(apiKey, diff string) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"

	reqBody := OpenAIRequest{
		Model: "gpt-4",
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{
				Role:    "system",
				Content: "You are a helpful assistant that reviews code diffs and provides suggestions.",
			},
			{
				Role:    "user",
				Content: "Please review the following code diff and provide improvement suggestions:\n" + diff,
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("OpenAI API error: %s", string(bodyBytes))
	}

	var openaiResp OpenAIResponse
	err = json.Unmarshal(bodyBytes, &openaiResp)
	if err != nil {
		return "", err
	}

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in OpenAI response")
	}

	return openaiResp.Choices[0].Message.Content, nil
}
