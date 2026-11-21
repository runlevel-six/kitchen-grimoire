// SPDX-License-Identifier: Apache-2.0

// Package metrics is a small, hand-rolled Prometheus exposition (§14.5). It
// offers just what the app needs: labelled counters, a labelled histogram,
// settable gauges, and gauges computed by a callback at scrape time. Output
// is the Prometheus text format, version 0.0.4.
//
// We write this ourselves instead of importing prometheus/client_golang to
// keep the dependency budget small (§5.2); the text format is simple and
// stable.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContentType is the media type of the text exposition format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Label is a name="value" pair attached to every sample of a metric.
type Label struct {
	Name, Value string
}

// collector is one metric family: it writes its HELP, TYPE and samples.
type collector interface {
	name() string
	write(ctx context.Context, w *bufio.Writer)
}

// Registry holds metric families and writes them out in name order.
// The zero value is not usable; call NewRegistry.
type Registry struct {
	mu         sync.Mutex
	collectors map[string]collector

	// HTTP metrics are created on first use by Middleware.
	httpOnce     sync.Once
	httpRequests *CounterVec
	httpDuration *HistogramVec
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{collectors: map[string]collector{}}
}

// register adds c, panicking on a duplicate name. Registration happens at
// start-up, so a duplicate is a programming error, like http.ServeMux's
// duplicate pattern panic.
func (r *Registry) register(c collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.collectors[c.name()]; dup {
		panic("metrics: duplicate metric " + c.name())
	}
	r.collectors[c.name()] = c
}

// WriteText writes every metric in the text exposition format. Callback
// gauges are evaluated now, with ctx.
func (r *Registry) WriteText(ctx context.Context, w io.Writer) error {
	r.mu.Lock()
	cs := make([]collector, 0, len(r.collectors))
	for _, c := range r.collectors {
		cs = append(cs, c)
	}
	r.mu.Unlock()
	slices.SortFunc(cs, func(a, b collector) int { return strings.Compare(a.name(), b.name()) })

	bw := bufio.NewWriter(w)
	for _, c := range cs {
		c.write(ctx, bw)
	}
	return bw.Flush()
}

// Handler serves the registry for a Prometheus scrape.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", ContentType)
		w.Header().Set("Cache-Control", "no-store")
		_ = r.WriteText(req.Context(), w)
	})
}

// ---- counters ----

// CounterVec is a counter with zero or more labels. Each distinct set of
// label values is its own series.
type CounterVec struct {
	family
	mu     sync.Mutex
	values map[string]*counterSeries // key: joined label values
}

type counterSeries struct {
	labels []string
	value  float64
}

// Counter registers a counter. labelNames may be empty.
func (r *Registry) Counter(name, help string, labelNames ...string) *CounterVec {
	c := &CounterVec{family: family{n: name, help: help, typ: "counter", labels: labelNames},
		values: map[string]*counterSeries{}}
	r.register(c)
	return c
}

// Inc adds 1 to the series for labelValues.
func (c *CounterVec) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add adds v (which must not be negative) to the series for labelValues.
// It panics if the number of label values is wrong.
func (c *CounterVec) Add(v float64, labelValues ...string) {
	if v < 0 {
		panic("metrics: counter " + c.n + " cannot decrease")
	}
	key := c.key(labelValues)
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.values[key]
	if s == nil {
		s = &counterSeries{labels: slices.Clone(labelValues)}
		c.values[key] = s
	}
	s.value += v
}

func (c *CounterVec) write(_ context.Context, w *bufio.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.header(w)
	for _, k := range sortedKeys(c.values) {
		s := c.values[k]
		writeSample(w, c.n, c.labels, s.labels, nil, s.value)
	}
}

// ---- histograms ----

// DefaultBuckets suit HTTP request latencies in seconds: 5 ms to 10 s.
var DefaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// HistogramVec is a histogram with zero or more labels.
type HistogramVec struct {
	family
	buckets []float64 // upper bounds, ascending, without +Inf
	mu      sync.Mutex
	values  map[string]*histSeries
}

type histSeries struct {
	labels []string
	counts []uint64 // per bucket, not cumulative; the last is +Inf
	sum    float64
	count  uint64
}

// Histogram registers a histogram with the given bucket upper bounds
// (ascending; +Inf is added automatically).
func (r *Registry) Histogram(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	if !slices.IsSorted(buckets) {
		panic("metrics: histogram " + name + " buckets are not sorted")
	}
	h := &HistogramVec{family: family{n: name, help: help, typ: "histogram", labels: labelNames},
		buckets: slices.Clone(buckets), values: map[string]*histSeries{}}
	r.register(h)
	return h
}

// Observe records one value in the series for labelValues.
func (h *HistogramVec) Observe(v float64, labelValues ...string) {
	key := h.key(labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.values[key]
	if s == nil {
		s = &histSeries{labels: slices.Clone(labelValues), counts: make([]uint64, len(h.buckets)+1)}
		h.values[key] = s
	}
	// The first bucket whose upper bound is >= v; len(buckets) means +Inf.
	i, _ := slices.BinarySearch(h.buckets, v)
	s.counts[i]++
	s.sum += v
	s.count++
}

func (h *HistogramVec) write(_ context.Context, w *bufio.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.header(w)
	leNames := append(slices.Clone(h.labels), "le")
	for _, k := range sortedKeys(h.values) {
		s := h.values[k]
		// Prometheus buckets are cumulative: each counts every value <= le.
		var cum uint64
		for i, c := range s.counts {
			cum += c
			le := "+Inf"
			if i < len(h.buckets) {
				le = formatFloat(h.buckets[i])
			}
			writeSample(w, h.n+"_bucket", leNames, append(slices.Clone(s.labels), le), nil, float64(cum))
		}
		writeSample(w, h.n+"_sum", h.labels, s.labels, nil, s.sum)
		writeSample(w, h.n+"_count", h.labels, s.labels, nil, float64(s.count))
	}
}

// ---- gauges ----

// Gauge is a single settable value without labels.
type Gauge struct {
	family
	mu    sync.Mutex
	value float64
	set   bool // unset gauges are not exported, rather than exported as 0
}

// Gauge registers a settable gauge. It is not exported until first Set,
// so "never happened" is distinguishable from a real zero.
func (r *Registry) Gauge(name, help string) *Gauge {
	g := &Gauge{family: family{n: name, help: help, typ: "gauge"}}
	r.register(g)
	return g
}

// Set sets the gauge's value.
func (g *Gauge) Set(v float64) {
	g.mu.Lock()
	g.value, g.set = v, true
	g.mu.Unlock()
}

// SetToCurrentTime sets the gauge to the current Unix time in seconds.
func (g *Gauge) SetToCurrentTime() { g.Set(float64(time.Now().UnixNano()) / 1e9) }

func (g *Gauge) write(_ context.Context, w *bufio.Writer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.header(w)
	if g.set {
		writeSample(w, g.n, nil, nil, nil, g.value)
	}
}

// gaugeFunc is a gauge whose value is computed at scrape time.
type gaugeFunc struct {
	family
	constLabels []Label
	fn          func(context.Context) (float64, error)
}

// GaugeFunc registers a gauge computed by fn on every scrape, with optional
// constant labels. If fn returns an error the sample is left out of that
// scrape (Prometheus then sees the series as absent) rather than reporting
// a misleading value.
func (r *Registry) GaugeFunc(name, help string, fn func(context.Context) (float64, error), constLabels ...Label) {
	r.register(&gaugeFunc{family: family{n: name, help: help, typ: "gauge"}, constLabels: constLabels, fn: fn})
}

func (g *gaugeFunc) write(ctx context.Context, w *bufio.Writer) {
	g.header(w)
	v, err := g.fn(ctx)
	if err != nil {
		return
	}
	writeSample(w, g.n, nil, nil, g.constLabels, v)
}

// ---- shared helpers ----

// family holds what every metric has: a name, help text, type, and the
// names of its variable labels.
type family struct {
	n, help, typ string
	labels       []string
}

func (f *family) name() string { return f.n }

func (f *family) header(w *bufio.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.n, escapeHelp(f.help), f.n, f.typ)
}

// key joins label values into a map key. The separator (0xff) can't occur
// in valid UTF-8, so distinct value lists never collide.
func (f *family) key(values []string) string {
	if len(values) != len(f.labels) {
		panic(fmt.Sprintf("metrics: %s wants %d label values, got %d", f.n, len(f.labels), len(values)))
	}
	return strings.Join(values, "\xff")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// writeSample writes one line: name{l1="v1",...} value.
func writeSample(w *bufio.Writer, name string, labelNames, labelValues []string, constLabels []Label, v float64) {
	w.WriteString(name)
	if len(labelNames)+len(constLabels) > 0 {
		w.WriteByte('{')
		first := true
		emit := func(n, val string) {
			if !first {
				w.WriteByte(',')
			}
			first = false
			w.WriteString(n)
			w.WriteString(`="`)
			w.WriteString(escapeLabel(val))
			w.WriteByte('"')
		}
		for _, l := range constLabels {
			emit(l.Name, l.Value)
		}
		for i, n := range labelNames {
			emit(n, labelValues[i])
		}
		w.WriteByte('}')
	}
	w.WriteByte(' ')
	w.WriteString(formatFloat(v))
	w.WriteByte('\n')
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, +1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// The text format escapes backslash and newline in HELP, and additionally
// the double quote in label values.
var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
func escapeLabel(s string) string { return labelEscaper.Replace(s) }
