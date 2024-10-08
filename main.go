package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/urfave/cli/v2"
)

type list struct {
	Repository string   `json:"Repository"`
	Tags       []string `json:"Tags"`
}

var FinalTags []string

func main() {
	app := &cli.App{
		Name:  "mirror-sync",
		Usage: "Synchronize Docker images",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "mirror",
				Value: "https://ghp.ci/https://raw.githubusercontent.com/cnsync/mirror-sync/main/private-mirrors.txt",
				Usage: "name storage address of the mirror to be synchronized",
			},
			&cli.IntFlag{
				Name:  "concurrency",
				Value: 10,
				Usage: "number of concurrent requests",
			},
			&cli.StringFlag{
				Name:  "hub",
				Value: "registry.cn-hangzhou.aliyuncs.com/grove",
				Usage: "destination hub for synchronization",
			},
		},
		Action: func(c *cli.Context) error {
			body := httpclient(c.String("mirror"))
			lines := strings.Split(body, "\n")

			// 去除数组中的空行
			var nonEmptyLines []string
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					nonEmptyLines = append(nonEmptyLines, line)
				}
			}

			syncImages(nonEmptyLines, c.String("hub"), c.Int("concurrency"))
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func syncImages(mirrorCtx []string, hub string, maxConcurrency int) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrency)

	for _, cmd := range mirrorCtx {
		// 获取源镜像的仓库和标签列表
		srcRepo, srcTags := listTags(cmd)

		if srcRepo == "" {
			log.Println("Empty tags for command:", cmd)
		} else {
			// 获取源镜像和目标镜像的名称
			srcRe, destRe := getImageNames(srcRepo, hub)
			_, destTag := listTags(destRe)

			if destTag != nil {
				// 去除重复的标签，并获取有效的标签列表
				FinalTags = removeDuplicates(srcTags, destTag)
				for _, tag := range getValidTags(FinalTags) {
					wg.Add(1)
					semaphore <- struct{}{} // 申请一个信号量，限制并发数
					go func(src, dest, t string) {
						defer func() {
							<-semaphore // 释放信号量
							wg.Done()
						}()
						copyImage(src, dest, t)
					}(srcRe, destRe, tag)
				}
			} else {
				for _, tag := range getValidTags(srcTags) {
					wg.Add(1)
					semaphore <- struct{}{} // 申请一个信号量，限制并发数
					go func(src, dest, t string) {
						defer func() {
							<-semaphore // 释放信号量
							wg.Done()
						}()
						copyImage(src, dest, t)
					}(srcRe, destRe, tag)
				}
			}
		}
	}

	wg.Wait()
	close(semaphore)
}

func getValidTags(tags []string) []string {
	var validTags []string
	// 定义正则表达式
	// 匹配 40 位十六进制字符串，允许后缀 3d5394a7e7072bc7754e5ce071bc6661d07da3e5  or 05e1a576b6726093a16e74fa31ef133f7a1ac6df-***
	reHex := regexp.MustCompile(`^[a-f0-9]{40}(-[a-zA-Z0-9]+)?$`)

	// 匹配任意字符后接40位十六进制字符串 amd64-0c1a1a690a12a50a35455ad8407c42edcf106ea0
	reCustom := regexp.MustCompile(`^.+-[a-f0-9]{40}$`)

	// 匹配日期时间格式，支持不同小数位 2020-01-13_11-17-25.346_PST
	reDateTime := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}(\.\d{1,3})?_[A-Z]{3}$`)

	for _, tag := range tags {
		if !strings.Contains(tag, ".sig") && // sha256-5415254e5d2545e2cf1256c17785a963f7e37a1f50cd251ba1da2a32a9fbb09d.sig
			!strings.Contains(tag, "sha256-") && // sha256-5415254e5d2545e2cf1256c17785a963f7e37a1f50cd251ba1da2a32a9fbb09d
			!strings.Contains(tag, ".att") &&
			!strings.Contains(tag, "arm") &&
			!strings.Contains(tag, "arm64") &&
			!strings.Contains(tag, "windows") &&
			!strings.Contains(tag, "nanoserver") &&
			!strings.Contains(tag, "windowsservercore") &&
			!reHex.MatchString(tag) &&
			!reDateTime.MatchString(tag) &&
			!reCustom.MatchString(tag) {
			validTags = append(validTags, tag)
		}
	}

	// 对标签列表进行排序
	sort.Slice(validTags, func(i, j int) bool {
		return validTags[i] < validTags[j]
	})

	return validTags
}

func listTags(image string) (string, []string) {
	var out bytes.Buffer

	if image != "" {
		// 使用skopeo命令获取镜像的标签信息
		cmd := exec.Command("skopeo", "list-tags", "docker://"+image)
		log.Println("Cmd", cmd.Args)
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			log.Println("exec.Command command execution error:", err)
			return "", nil
		}

		var l list
		err = json.Unmarshal(out.Bytes(), &l)
		if err != nil {
			log.Println("json.Unmarshal conversion error:", err)
		}

		return l.Repository, l.Tags
	}

	return "", nil
}

func getImageNames(srcRepo, hub string) (string, string) {
	beginIndex := strings.Index(srcRepo, "/")
	rightIndex := strings.LastIndex(srcRepo, "/")

	diffB := srcRepo[beginIndex:rightIndex]
	diffR := srcRepo[rightIndex:]

	if strings.EqualFold(diffB, "/library") {
		str := diffR[1:]
		return srcRepo, hub + "/" + str
	}

	if strings.EqualFold(diffB, diffR) {
		b1 := srcRepo[rightIndex+1:]
		b2 := strings.Replace(b1, "/", "-", -1)
		return srcRepo, hub + "/" + b2
	}

	b1 := srcRepo[beginIndex+1:]
	b2 := strings.Replace(b1, "/", "-", -1)

	return srcRepo, hub + "/" + b2
}

func httpclient(url string) string {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Get(url)
	if err != nil {
		log.Println("Error:", err)
		return ""
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			return
		}
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error:", err)
		return ""
	}
	return string(body)
}

func removeDuplicates(left, right []string) []string {
	set := make(map[string]bool)
	for _, r := range right {
		set[r] = true
	}

	var result []string
	for _, l := range left {
		if !set[l] {
			result = append(result, l)
		}
	}
	return result
}

func copyImage(srcRe, destRe, tag string) {
	cmd := exec.Command(
		"skopeo",
		"copy",
		"--insecure-policy",
		"--src-tls-verify=false",
		"--dest-tls-verify=false",
		"-q",
		"docker://"+srcRe+":"+tag,
		"docker://"+destRe+":"+tag)
	log.Println("Cmd", cmd.Args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("Error executing command %s: %s\n", cmd.Args, err)
	}
}
