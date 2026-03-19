package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	"github.com/named-data/ndnd/std/types/optional"
)

type JobStatusPayload struct {
	Status           string          `json:"status"` // processing | done | failed
	NextInterestName string          `json:"next_interest_name"`
	RetryAfter       string          `json:"retry_after"`
	Message          string          `json:"message,omitempty"`
	Result           json.RawMessage `json:"data,omitempty"`
}

func publishStatus(client ndn.Client, timeout time.Duration, currentName, jobNamePrefix enc.Name, resultCh <-chan []byte) {
	produceStatus := func(name enc.Name, payload []byte, period time.Duration) {
		_, err := client.Produce(ndn.ProduceArgs{
			Name:            name,
			Content:         enc.Wire{payload},
			FreshnessPeriod: period,
		})
		if err != nil {
			log.Error(client, "Failed to produce progress update", "err", err)
		}
	}

	go func() { // background goroutine to publish heartbeat until job is done

		ticker := time.NewTicker(timeout)
		defer ticker.Stop()
		nextName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)

		payload := JobStatusPayload{
			Status:           "processing",
			NextInterestName: nextName.String(),
			RetryAfter:       timeout.String(),
		}
		payloadBytes, _ := json.Marshal(payload)
		produceStatus(currentName, payloadBytes, timeout)

		currentName = nextName
		for {
			select {
			// once u get the final result
			case result := <-resultCh:
				produceStatus(currentName, result, 60*time.Second)
				return
			// ask client to check status after %d seconds
			case <-ticker.C:
				nextName = jobNamePrefix.WithVersion(enc.VersionUnixMicro)
				payload.NextInterestName = nextName.String()
				payloadBytes, _ := json.Marshal(payload)
				produceStatus(currentName, payloadBytes, timeout)
				currentName = nextName
			}
		}
	}()
}

func waitJobResult(client ndn.Client, jobName string) ([]byte, error) {
	fmt.Println("Start waiting for job done with job name:", jobName)

	jobInterest, err := enc.NameFromStr(jobName)
	if err != nil {
		return nil, fmt.Errorf("invalid job name: %w", err)
	}
	lastVer := uint64(0)

	checkTimer := time.NewTimer(0 * time.Second)
	defer checkTimer.Stop()

	fetchText := func(name enc.Name) (string, error) {
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)

		client.ExpressR(ndn.ExpressRArgs{
			Name: name,
			Config: &ndn.InterestConfig{
				CanBePrefix: true,
				MustBeFresh: true,
				Lifetime:    optional.Some(2 * time.Second),
			},
			Retries: 2,
			Callback: func(args ndn.ExpressCallbackArgs) {
				if args.Result != ndn.InterestResultData {
					errCh <- fmt.Errorf("%s", args.Result)
					return
				}
				resultCh <- string(args.Data.Content().Join())
			},
		})

		select {
		case s := <-resultCh:
			return s, nil
		case err := <-errCh:
			return "", err
		}
	}

	for range checkTimer.C {
		fmt.Println("Checking job status...", jobInterest.String())

		status, err := fetchText(jobInterest)
		if err != nil {
			fmt.Printf("status check failed: %v\n", err)
			return nil, fmt.Errorf("wait job timeout: %s", jobName)
		}
		var st JobStatusPayload
		if err := json.Unmarshal([]byte(status), &st); err != nil {
			return nil, fmt.Errorf("invalid status payload: %w raw=%q", err, status)
		}

		switch st.Status {
		case "success":
			fmt.Println("job done")
			return st.Result, nil

		case "failed":
			return nil, fmt.Errorf("job failed: %s", st.Message)

		case "processing":
			println("job processing, will check again after", st.RetryAfter)
			println("next interest name for status check:", st.NextInterestName)
			checkTimeout, err := time.ParseDuration(st.RetryAfter)
			if err != nil {
				return nil, fmt.Errorf("failed to parse processing time: %v\n", err)
			}
			nextName, err := enc.NameFromStr(strings.TrimSpace(st.NextInterestName))
			if err != nil {
				return nil, fmt.Errorf("invalid next_interest_name %q: %w", st.NextInterestName, err)
			}
			ver := nextName.At(-1).NumberVal()
			if ver <= lastVer {
				return nil, fmt.Errorf("next interest version not increasing: %d <= %d", ver, lastVer)
			}
			jobInterest = nextName
			checkTimer.Reset(checkTimeout)

		default:
			return nil, fmt.Errorf("unexpected status: %s", st.Status)
		}
	}
	return nil, fmt.Errorf("status loop ended unexpectedly")
}
