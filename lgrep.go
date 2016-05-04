package lgrep

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"gopkg.in/olivere/elastic.v3"
)

var (
	// ErrEmptySearch is returned when an empty query is given.
	ErrEmptySearch = errors.New("Empty search query, not submitting.")
	// DefaultSpec provides a reasonable default search specification.
	DefaultSpec = SearchOptions{Size: 100, SortTime: SortDesc}
)

// LGrep holds state and configuration for running queries against the
type LGrep struct {
	// Client is the backing interface that searches elasticsearch
	*elastic.Client
	// Endpoint to use when working with Elasticsearch
	Endpoint string
}

// New creates a new lgrep client.
func New(endpoint string) (lg LGrep, err error) {
	lg = LGrep{Endpoint: endpoint}
	lg.Client, err = elastic.NewClient(elastic.SetURL(endpoint))
	return lg, err
}

// SearchOptions is used to apply provided options to a search that is
// to be performed.
type SearchOptions struct {
	// Size is the number of records to be returned.
	Size int
	// Index is a single index to search
	Index string
	// Indicies are the indicies that are to be searched
	Indices []string
	// SortTime causes the query to be sorted by the appropriate
	// timestamp field
	SortTime *bool
	// QueryDebug prints out the resulting query on the console if set
	QueryDebug bool
}

// apply the options given in the search specification to an already
// instaniated search.
func (s SearchOptions) apply(search *elastic.SearchService) {
	if s.Size != 0 {
		search.Size(s.Size)
	}
	if s.Index != "" {
		search.Index(s.Index)
	}
	if len(s.Indices) != 0 {
		search.Index(s.Indices...)
	}
	if s.SortTime != nil {
		SortByTimestamp(search, *s.SortTime)
	}
}

// SimpleSearch runs a lucene search configured by the SearchOption
// specification.
func (l LGrep) SimpleSearch(q string, spec *SearchOptions) (docs []*json.RawMessage, err error) {
	if q == "" {
		return docs, ErrEmptySearch
	}
	docs = make([]*json.RawMessage, 0)
	search, dbg := l.NewSearch()
	search = SearchWithLucene(search, q)
	if spec != nil {
		// If user wants 0 then they're really not looking to get any
		// results, don't execute.
		if spec.Size == 0 {
			return docs, err
		}
	} else {
		spec = &DefaultSpec
	}
	spec.apply(search)

	// Spit out the query that will be sent.
	if spec.QueryDebug {
		dbg(os.Stderr)
	}
	log.Debug("Submitting search request..")
	res, err := search.Do()
	if err != nil {
		return docs, errors.Annotatef(err, "Search returned with error")
	}
	for _, doc := range res.Hits.Hits {
		docs = append(docs, doc.Source)
	}
	return docs, nil
}

// SearchWithSource may be used to provide a pre-contstructed json
// query body when a query cannot easily be formed with the available
// methods. The applied SearchOptions specification *is not fully
// compatible* with a manually crafted query body but some options are
// - see SearchOptions for any caveats.
func (l LGrep) SearchWithSource(source interface{}, spec *SearchOptions) (docs []*json.RawMessage, err error) {
	docs = make([]*json.RawMessage, 0)

	search, dbg := l.NewSearch()
	if spec != nil {
		// If user wants 0 then they're really not looking to get any
		// results, don't execute.
		if spec.Size == 0 {
			return docs, err
		}
	} else {
		spec = &DefaultSpec
	}
	spec.apply(search)

	if spec.QueryDebug {
		dbg(os.Stderr)
	}

	log.Debug("Submitting search request..")
	res, err := search.Do()
	if err != nil {
		return docs, errors.Annotatef(err, "Search returned with error")
	}
	for _, doc := range res.Hits.Hits {
		docs = append(docs, doc.Source)
	}
	return docs, nil
}

// SearchTimerange will return occurrences of the matching search in
// the timeframe provided.
func (l LGrep) SearchTimerange(search string, count int, t1 time.Time, t2 time.Time) {

}

// NewSearch initializes a new search object along with a func to
// debug the resulting query to be sent.
func (l LGrep) NewSearch() (search *elastic.SearchService, dbg func(wr io.Writer)) {
	source := elastic.NewSearchSource()
	search = l.Client.Search().SearchSource(source)

	// Debug the query that's produced by the search parameters
	dbg = func(wr io.Writer) {
		queryMap, err := source.Source()
		if err == nil {
			queryJSON, err := json.MarshalIndent(queryMap, "q> ", "  ")
			if err == nil {
				fmt.Fprintf(wr, "q> %s\n", queryJSON)
			}
		}
	}
	return search, dbg
}
