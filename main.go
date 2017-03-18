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
	// Matches something like "1 minute ago" or "3 hours ago". Note we include
	// some angle brackets to avoid false positives.
	timeRegexp = regexp.MustCompile(`>([1-9]\d*) (\w+) ago<`)
)

type Conf struct {
	Domain []string
}

func main() {
	conf, err := parseConf()
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}

	for {
		for _, domain := range conf.Domain {
			url := fmt.Sprintf(hnDomainURL, domain)
			fmt.Printf("Requesting: %v\n", url)

			resp, err := http.Get(url)
			if err != nil {
				fmt.Printf("Error while requesting \"%s\": %s\n", url, err.Error())
				continue
			}
			if resp.StatusCode != 200 {
				fmt.Printf("Bad status while requesting \"%s\": %v\n", url, resp.StatusCode)
				continue
			}

			respBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Printf("Error while reading response from \"%s\": %v\n", url, err.Error())
				continue
			}

			matches := timeRegexp.FindAllStringSubmatch(string(respBytes), -1)
			for _, match := range matches {
				numStr := match[1]
				unit := match[2]

				num, err := strconv.Atoi(numStr)
				if err != nil {
					fmt.Printf("Error while parsing number \"%s\": %v\n", num, err.Error())
					continue
				}

				duration, err := parseDuration(num, unit)
				if err != nil {
					fmt.Printf("Error while parsing duration: %v\n", err.Error())
					continue
				}

				fmt.Printf("Found an article with age: %v\n", duration)

				if duration <= alertPeriod {
					fmt.Printf("ALERT! Article's age is below alert threshold.")
				}
			}
		}

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

func parseConf() (*Conf, error) {
	conf := &Conf{}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil, fmt.Errorf("Need value for: DOMAIN")
	}
	conf.Domain = strings.Split(domain, ",")

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
