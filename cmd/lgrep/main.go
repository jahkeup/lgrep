package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/cogolabs/lgrep"
	"github.com/juju/errors"
)

const (
	// DefaultFormat provides a sane default to use in the case that the
	// user does not provide a format.
	DefaultFormat = ".message"
	// StdlineFormat provides a common usable format
	StdlineFormat = ".host .service .message"
)

var (
	flagEndpoint = flag.String("e", "http://localhost:9200/", "Elasticsearch endpoint")
	flagDebug    = flag.Bool("D", false, "Debug lgrep")

	flagQueryIndex = flag.String("Qi", "", "Index to search")
	flagQueryDebug = flag.Bool("QD", false, "Debug the query sent to elasticsearch")
	//flagQuerySort  = flag.String("Qs", "timestamp:desc", "Sort by <field>:<asc|desc> (appended when specified)")
	//flagQueryRegex = flag.String("Qr", "message:^.*$", "Add a regex query to the search (AND'd)")

	flagResultCount         = flag.Int("n", 100, "Number of results to fetch")
	flagResultFormat        = flag.String("f", DefaultFormat, "Format returned results into text/template format")
	flagResultVerboseFormat = flag.Bool("vf", false, "Use a default verbose format")
	flagResultFields        = flag.String("c", "", "Fields to return, causes results to be rendered as json")
	flagResultTabulate      = flag.Bool("T", false, "Write out as tabulated data")
)

var (
	// GlobalFlags apply to the entire application
	GlobalFlags = []cli.Flag{
		cli.StringFlag{
			Name:   "endpoint, e",
			Value:  "http://localhost:9200/",
			Usage:  "Elasticsearch Endpoint",
			EnvVar: "LGREP_ENDPOINT",
		},

		cli.BoolFlag{
			Name:  "debug, D",
			Usage: "Debug lgrep run with verbose logging",
		},
	}

	// QueryFlags apply to runs that query with lgrep
	QueryFlags = []cli.Flag{
		cli.IntFlag{
			Name:  "size, n",
			Usage: "Number of results to be returned",
			Value: 100,
		},
		cli.StringFlag{
			Name:  "format, f",
			Usage: "Simple formatting using text/template (go stdlib)",
			Value: DefaultFormat,
		},
		cli.BoolFlag{
			Name:  "raw-json, j",
			Usage: "Output the raw json _source of the results (1 line per result)",
		},
		cli.BoolFlag{
			Name:  "stdline, ff",
			Usage: "Format lines with common format '" + StdlineFormat + "'.",
		},
		cli.BoolFlag{
			Name:  "tabulate, T",
			Usage: "Tabulate the data into columns",
		},
		cli.BoolFlag{
			Name:   "query-debug, QD",
			Usage:  "Log query sent to the server",
			Hidden: true,
		},
		cli.StringFlag{
			Name:  "query-index, Qi",
			Usage: "Query this index in elasticsearch, if not provided - all indicies",
		},
		cli.StringFlag{
			Name:  "query-fields, Qc",
			Usage: "Fields to be retrieved (ex: field1,field2)",
		},
		cli.StringFlag{
			Name:  "query-file, Qf",
			Usage: "Raw elasticsearch json query to submit",
		},
	}
)

func App() *cli.App {
	app := cli.NewApp()
	app.Name = "lgrep"
	app.Version = "1.0.0"
	app.EnableBashCompletion = true

	// Set up the application based on flags before handing off to the action
	app.Before = RunPrepareApp
	app.Action = RunQuery
	app.UsageText = "lgrep [options] QUERY"
	app.After = func(c *cli.Context) error {
		for _, f := range c.GlobalFlagNames() {
			fmt.Printf("%s = %s\n", f, c.Generic(f))
		}
		return nil
	}
	app.Flags = append(app.Flags, GlobalFlags...)
	app.Flags = append(app.Flags, QueryFlags...)
	app.Usage = `

Reference time: Mon Jan 2 15:04:05 -0700 MST 2006

Text formatting
given: { "timestamp": "2016-04-29T13:58:59.420Z" }

{{.timestamp|ftime "15:04"}} => 13:58
{{.timestamp|ftime "2006-01-02 15:04"}} => 2016-04-29 13:58
`
	return app
}

// RunPrepareApp sets defaults and verifies the arguments and flags
// passed to the application.
func RunPrepareApp(c *cli.Context) (err error) {
	// query might have been provided via a file or another flag
	var queryProvided bool

	if endpoint := c.String("endpoint"); endpoint == "" {
		return cli.NewExitError("Endpoint must be set", 1)
	} else if _, err := url.Parse(endpoint); err != nil {
		return cli.NewExitError("Endpoint must be a url (ex: http://localhost:9200/)", 1)
	}

	// Set the format to the stdline format if asked, and warn when
	// they're both set.
	if c.Bool("stdline") {
		if c.IsSet("format") {
			log.Warn("You've provided a format (-f) and asked for the stdline format (-ff), using stdline!")
		}
		c.Set("format", StdlineFormat)
	}

	if c.Bool("debug") {
		log.SetLevel(log.DebugLevel)
	}

	if c.IsSet("query-file") {
		if _, err := os.Stat(c.String("query-file")); err != nil {
			return cli.NewExitError("Query file provided cannot be read", 3)
		}
		queryProvided = true
	}

	// Can't provide both a query via a file and via lucene search via
	// args.
	if len(c.Args()) > 0 && queryProvided {
		return cli.NewExitError("You've provided multiple queries (file and lucene perhaps?)", 3)
	}
	if len(c.Args()) == 0 && !queryProvided {
		return cli.NewExitError("No query provided", 3)
	}

	return err
}

func RunQuery(c *cli.Context) (err error) {
	var (
		endpoint    = c.String("endpoint")
		queryFile   = c.String("query-file")
		querySize   = c.Int("count")
		queryIndex  = c.String("query-index")
		queryDebug  = c.Bool("query-debug")
		queryFields = strings.Split(c.String("query-fields"), ",")
		query       = strings.Join(c.Args(), " ")

		format    = c.String("format")
		formatRaw = c.Bool("raw-json")

		// Results from the executed search
		results []*json.RawMessage
	)

	l, err := lgrep.New(endpoint)
	if err != nil {
		return err
	}

	l.Debug = queryDebug

	if c.IsSet("query-file") {
		var (
			f *os.File
			d []byte
		)
		f, err = os.Open(queryFile)
		if err != nil {
			return errors.Annotate(err, "Could not open the provided query file")
		}
		d, err = ioutil.ReadAll(f)
		if err != nil {
			return errors.Annotate(err, "Could not read the provided query file")
		}
		results, err = l.SearchWithSource(d)
	}

	if query != "" {
		results, err = l.SimpleSearch(query, queryIndex, querySize)
	}

	if err != nil {
		return err
	}

	if len(results) == 0 {
		log.Warn("0 results returned")
		return
	}

	if formatRaw {
		if len(queryFields) > 0 {
			log.Error("Field selection and raw output is unsupported at this time")
			return nil
		}
		for i := range results {
			fmt.Printf("%s\n", results[i])
		}
		return
	}

	msgs, err := lgrep.Format(results, format)
	if err != nil {
		return err
	}
	for i := range msgs {
		fmt.Println(msgs[i])
	}
	return nil
}

func main() {
	App().Run(os.Args)
}
