package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	alertPeriod = 10 * time.Minute
	hnDomainURL = "https://news.ycombinator.com/from?site=%s"
)

var (
	conf *Conf

	// Matches something like "1 minute ago" or "3 hours ago". Note we include
	// some angle brackets to avoid false positives.
	timeRegexp = regexp.MustCompile(`>([1-9]\d*) (\w+) ago<`)
)

type Conf struct {
	// Domain is specified as DOMAIN and may included multiple domains to check
	// separated by a comma.
	Domain []string

	SMTPLogin    string
	SMTPPassword string
	SMTPPort     string
	SMTPServer   string
}

func main() {
	var err error
	conf, err = parseConf()
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}

	for {
		err = checkDomains()
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			goto wait
		}

	wait:
		// Add some random jitter just so that we're not requesting on a
		// perfectly predictable schedule all the time.
		sleepDuration := alertPeriod - time.Duration(rand.Intn(60))*time.Second
		fmt.Printf("Sleeping for %v between runs\n", sleepDuration)
		time.Sleep(sleepDuration)
	}
}

//
// Helpers
//

func checkDomains() error {
	for _, domain := range conf.Domain {
		url := fmt.Sprintf(hnDomainURL, domain)
		respData, err := getHTTPData(url)
		if err != nil {
			return err
		}

		durations, err := parseDurations(string(respData))
		if err != nil {
			return err
		}

		for _, duration := range durations {
			fmt.Printf("Found an article with age: %v\n", duration)

			if duration <= alertPeriod {
				fmt.Printf("ALERT! Article's age is below alert threshold.")
			}
		}
	}

	return nil
}

func getHTTPData(url string) ([]byte, error) {
	fmt.Printf("Requesting: %v\n", url)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Error while requesting \"%s\": %s\n", url, err.Error())
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Bad status while requesting \"%s\": %v\n", url, resp.StatusCode)
	}

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Error while reading response from \"%s\": %v\n", url, err.Error())
	}

	return respBytes, nil
}

func parseConf() (*Conf, error) {
	conf := &Conf{}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil, fmt.Errorf("Need value for: DOMAIN")
	}
	conf.Domain = strings.Split(domain, ",")

	conf.SMTPLogin = os.Getenv("MAILGUN_SMTP_LOGIN")
	if conf.SMTPLogin == "" {
		return nil, fmt.Errorf("Need value for: MAILGUN_SMTP_LOGIN")
	}

	conf.SMTPPassword = os.Getenv("MAILGUN_SMTP_PASSWORD")
	if conf.SMTPPassword == "" {
		return nil, fmt.Errorf("Need value for: MAILGUN_SMTP_PASSWORD")
	}

	conf.SMTPPort = os.Getenv("MAILGUN_SMTP_PORT")
	if conf.SMTPPort == "" {
		return nil, fmt.Errorf("Need value for: MAILGUN_SMTP_PORT")
	}

	conf.SMTPServer = os.Getenv("MAILGUN_SMTP_SERVER")
	if conf.SMTPServer == "" {
		return nil, fmt.Errorf("Need value for: MAILGUN_SMTP_SERVER")
	}

	return conf, nil
}

func parseDuration(num int, unit string) (time.Duration, error) {
	// So I'm pretty sure HN only goes from minutes to days units, but just
	// handle everything in case that changes at some point.
	switch unit {
	case "second":
	case "seconds":
		return time.Duration(num) * time.Second, nil

	case "minute":
	case "minutes":
		return time.Duration(num) * time.Minute, nil

	case "hour":
	case "hours":
		return time.Duration(num) * time.Hour, nil

	case "day":
	case "days":
		return time.Duration(num) * time.Hour * 24, nil

	case "month":
	case "months":
		return time.Duration(num) * time.Hour * 24 * 30, nil

	case "year":
	case "years":
		return time.Duration(num) * time.Hour * 24 * 365, nil
	}

	return 0 * time.Second, fmt.Errorf("Couldn't parse duration: %v %v", num, unit)
}

func parseDurations(content string) ([]time.Duration, error) {
	var durations []time.Duration
	matches := timeRegexp.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		numStr := match[1]
		unit := match[2]

		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("Error while parsing number \"%s\": %v\n", num, err.Error())
			continue
		}

		duration, err := parseDuration(num, unit)
		if err != nil {
			return nil, fmt.Errorf("Error while parsing duration: %v\n", err.Error())
		}

		durations = append(durations, duration)
	}
	return durations, nil
}
