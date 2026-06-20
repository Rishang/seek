package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rishang/seek/config"
	"github.com/urfave/cli/v3"
)

// outputFlag selects how results are printed. Distinct from scrape's --format,
// which controls the page-content representation requested from the provider.
var outputFlag = &cli.StringFlag{
	Name:    "output",
	Aliases: []string{"o"},
	Value:   "json",
	Usage:   "Output format: json, csv",
}

// render prints v in the format selected by --output.
func render(cmd *cli.Command, v any) error {
	return renderTo(os.Stdout, cmd.String("output"), v)
}

// renderTo writes v to w in the given format. CSV layouts are type-specific;
// anything else (or an unknown value) falls back to JSON.
func renderTo(w io.Writer, format string, v any) error {
	if format != "csv" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	switch r := v.(type) {
	case []config.SearchResult:
		cw.Write([]string{"title", "url", "snippet", "published_date"})
		for _, item := range r {
			cw.Write([]string{item.Title, item.URL, item.Snippet, item.PublishedDate})
		}
	case *config.CrawlResult:
		cw.Write([]string{"url", "pages", "content"})
		cw.Write([]string{r.URL, strings.Join(r.Pages, "\n"), r.Content})
	default:
		return fmt.Errorf("csv output not supported for this result type")
	}
	cw.Flush()
	return cw.Error()
}
