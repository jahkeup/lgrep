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
	// ErrZeroSize is returned when a query is made with a Size spec of 0.
	ErrZeroSize = errors.New("Search was for a 0 size, not executing.")
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

// SimpleSearchStream configures and executes a search stream using a lucene query.
func (l LGrep) SimpleSearchStream(q string, spec *SearchOptions) (stream *SearchStream, err error) {
	if q == "" {
		return nil, ErrEmptySearch
	}
	search, source := l.NewSearch()
	search = SearchWithLucene(search, q)
	if spec != nil {
		// If user wants 0 then they're really not looking to get any
		// results, don't execute.
		if spec.Size == 0 {
			return nil, ErrZeroSize
		}
	} else {
		spec = &DefaultSpec
	}

	spec.configureSearch(search)

	// Spit out the query that will be sent.
	if spec.QueryDebug {
		query, err := source.Source()
		if err != nil {
			log.Error(errors.Annotate(err, "Error generating query source, may indicate further issues."))
		}
		printQueryDebug(os.Stderr, query)
	}

	if !spec.QuerySkipValidate {
		log.Debug("Validating query..")
		_, err := l.validate(source, *spec)
		if err != nil {
			return nil, err
		}
	}

	return l.execute(search, source, *spec)
}

// SimpleSearch runs a lucene search configured by the SearchOption
// specification.
func (l LGrep) SimpleSearch(q string, spec *SearchOptions) (results []Result, err error) {
	stream, err := l.SimpleSearchStream(q, spec)
	if err != nil {
		return results, err
	}
	return stream.All()
}

// SearchWithSourceStream configures with a raw query and executes a
// search stream that can be read.
func (l LGrep) SearchWithSourceStream(raw interface{}, spec *SearchOptions) (stream *SearchStream, err error) {
	search, _ := l.NewSearch()
	if spec == nil {
		spec = &DefaultSpec
	}

	spec.configureSearch(search)
	var query elastic.Query
	switch v := raw.(type) {
	case json.RawMessage:
		query, err = QueryMapFromJSON(v)
	case []byte:
		data := json.RawMessage(v)
		query, err = QueryMapFromJSON(data)
	case map[string]interface{}:
		query = QueryMap(v)
	case QueryMap:
		query = v
	default:
		log.Fatalf("SearchWithSource does not support type '%T' at this time.", v)
	}
	// Set the search source to the provided raw one.
	source, _ := query.Source()
	search.Source(source)

	if spec.QueryDebug {
		printQueryDebug(os.Stderr, query)
	}

	if !spec.QuerySkipValidate {
		vresp, err := l.validate(query, *spec)
		if err != nil {
			if spec.QueryDebug {
				printQueryDebug(os.Stderr, vresp)
			}
			return nil, err
		}
	}

	return l.execute(search, query, *spec)
}

// SearchWithSource may be used to provide a pre-contstructed json
// query body when a query cannot easily be formed with the available
// methods. The applied SearchOptions specification *is not fully
// compatible* with a manually crafted query body but some options are
// - see SearchOptions for any caveats.
func (l LGrep) SearchWithSource(raw interface{}, spec *SearchOptions) (results []Result, err error) {
	stream, err := l.SearchWithSourceStream(raw, spec)
	if err != nil {
		return results, err
	}
	return stream.All()
}

//
func extractResult(hit *elastic.SearchHit, spec SearchOptions) (result Result, err error) {
	if spec.RawResult {
		return HitResult(*hit), nil
	}
	if len(spec.Fields) != 0 && len(hit.Fields) != 0 {
		return FieldResult(hit.Fields), nil
	}
	if hit == nil || hit.Source == nil {
		return nil, errors.New("nil document returned")
	}
	return SourceResult(*hit.Source), nil
}

// consumeResults ingests the results from the returned data and
// transforms them into Result's.
func consumeResults(res *elastic.SearchResult, spec SearchOptions) (results []Result, err error) {
	for _, doc := range res.Hits.Hits {
		result, err := extractResult(doc, spec)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// SearchTimerange will return occurrences of the matching search in
// the timeframe provided.
func (l LGrep) SearchTimerange(search string, count int, t1 time.Time, t2 time.Time) {

}

// NewSearch initializes a new search object along with a func to
// debug the resulting query to be sent.
func (l LGrep) NewSearch() (search *elastic.SearchService, source *elastic.SearchSource) {
	source = elastic.NewSearchSource()
	search = l.Client.Search().SearchSource(source)

	return search, source
}

// printQueryDebug prints out the formatted JSON query body that will
// be submitted.
func printQueryDebug(out io.Writer, query interface{}) {
	var (
		queryJSON []byte
		err       error
	)

	// json.RawMessage must be passed as a pointer to be Marshaled
	// correctly.
	if raw, ok := query.(json.RawMessage); ok {
		queryJSON, err = json.MarshalIndent(&raw, "q> ", "  ")
	} else {
		queryJSON, err = json.MarshalIndent(query, "q> ", "  ")
	}
	if err == nil {
		fmt.Fprintf(out, "q> %s\n", queryJSON)
	}
}
