package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/otknoy/echonet-lite/aircon"
)

// writeTargetResults preserves the legacy single-device and named result formats.
func writeTargetResults(out io.Writer, results []targetResult, named bool, format string) error {
	var err error
	switch {
	case named && format == "json":
		err = json.NewEncoder(out).Encode(results)
	case named:
		for _, r := range results {
			if r.Status != nil {
				err = writeStatus(out, r.Target+"\t", *r.Status)
			} else if r.Error != "" {
				_, err = fmt.Fprintf(out, "%s\terror: %s\n", r.Target, r.Error)
			} else {
				_, err = fmt.Fprintf(out, "%s\t%s\n", r.Target, r.Result)
			}
			if err != nil {
				break
			}
		}
	case len(results) == 1:
		r := results[0]
		if r.Status != nil {
			if format == "json" {
				err = json.NewEncoder(out).Encode(r.Status)
			} else {
				err = writeStatus(out, "", *r.Status)
			}
		} else if r.Result == "accepted" {
			if format == "json" {
				err = json.NewEncoder(out).Encode(struct {
					Status string `json:"status"`
				}{"accepted"})
			} else {
				_, err = fmt.Fprintln(out, "accepted")
			}
		}
	}
	return err
}

func writeStatus(out io.Writer, prefix string, status aircon.Status) error {
	_, err := fmt.Fprintf(out, "%spower: %s\n%smode: %s\n%stemperature: %s\n%sroom_temperature: %s\n", prefix, fieldText(status.Power), prefix, fieldText(status.Mode), prefix, fieldText(status.Temperature), prefix, fieldText(status.RoomTemperature))
	return err
}

func fieldText[T any](f aircon.Field[T]) string {
	if f.Value != nil {
		return fmt.Sprint(*f.Value)
	}
	if f.Error != "" {
		return f.State + ": " + f.Error
	}
	return f.State
}
