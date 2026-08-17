// Command loadgen drives the services hard enough to find out where they bend.
//
// It is deliberately small and dependency free: it signs in once, runs one
// scenario with a fixed number of workers for a fixed time, and reports the
// distribution of the latencies rather than an average, because an average
// hides exactly the requests a person would complain about.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var (
		scenario    = flag.String("scenario", "catalogue", "which scenario to run")
		concurrency = flag.Int("concurrency", 16, "how many requests are in flight at once")
		duration    = flag.Duration("duration", 20*time.Second, "how long to keep the load on")
		items       = flag.Int("items", 3, "items per invoice, for the scenarios that write one")
		batch       = flag.Int("batch", 20, "items per bulk call")
		stockURL    = flag.String("stock", "http://localhost:8081", "stock service base URL")
		billingURL  = flag.String("billing", "http://localhost:8082", "billing service base URL")
		identityURL = flag.String("identity", "http://localhost:8083", "identity service base URL")
		email       = flag.String("email", "ada@example.com", "account used to sign in")
		password    = flag.String("password", "correct horse battery staple", "password of that account")
		seedCount   = flag.Int("seed", 0, "when set, register this many products and exit")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        512,
			MaxIdleConnsPerHost: 512,
			MaxConnsPerHost:     512,
		},
	}

	runner := &runner{
		client:      client,
		stockURL:    *stockURL,
		billingURL:  *billingURL,
		identityURL: *identityURL,
		items:       *items,
		batch:       *batch,
	}

	if err := runner.signIn(ctx, *email, *password); err != nil {
		fail("sign in: %v", err)
	}

	if *seedCount > 0 {
		if err := runner.seed(ctx, *seedCount); err != nil {
			fail("seed: %v", err)
		}
		fmt.Printf("seeded %d products\n", *seedCount)
		return
	}

	if err := runner.loadProducts(ctx); err != nil {
		fail("read the catalogue: %v", err)
	}
	if len(runner.productIDs) == 0 {
		fail("the catalogue is empty; run with -seed first")
	}

	work, ok := scenarios[*scenario]
	if !ok {
		fail("unknown scenario %q; available: %s", *scenario, scenarioNames())
	}

	fmt.Printf("scenario=%s concurrency=%d duration=%s items=%d products=%d\n",
		*scenario, *concurrency, *duration, *items, len(runner.productIDs))

	report := runner.run(ctx, work, *concurrency, *duration)
	report.print()
}

// runner holds everything a scenario needs.
type runner struct {
	client      *http.Client
	stockURL    string
	billingURL  string
	identityURL string
	token       string
	items       int
	batch       int

	productIDs []string
	invoiceIDs []string
	mu         sync.Mutex
	nextSeed   atomic.Int64
}

type scenarioFunc func(ctx context.Context, r *runner, worker int) (int, error)

var scenarios = map[string]scenarioFunc{
	"catalogue":     scenarioCatalogue,
	"catalogue-out": scenarioCatalogueOutOfStock,
	"invoice-list":  scenarioInvoiceList,
	"invoice-read":  scenarioInvoiceRead,
	"invoice-write": scenarioInvoiceWrite,
	"bulk-adjust":   scenarioBulkAdjust,
	"bulk-create":   scenarioBulkCreate,
}

func scenarioNames() string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprint(names)
}

// scenarioCatalogue is the screen an operator leaves open: a filtered,
// paginated listing read over and over.
func scenarioCatalogue(ctx context.Context, r *runner, worker int) (int, error) {
	term := fmt.Sprintf("LOAD-%d", rand.Intn(1000))
	url := fmt.Sprintf("%s/products?search=%s&limit=20", r.stockURL, term)
	return r.do(ctx, http.MethodGet, url, nil, nil)
}

// scenarioCatalogueOutOfStock is the replenishment question: what is running
// out, ordered by balance.
func scenarioCatalogueOutOfStock(ctx context.Context, r *runner, worker int) (int, error) {
	url := fmt.Sprintf("%s/products?max_balance=5&sort=balance&order=asc&limit=20", r.stockURL)
	return r.do(ctx, http.MethodGet, url, nil, nil)
}

func scenarioInvoiceList(ctx context.Context, r *runner, worker int) (int, error) {
	url := fmt.Sprintf("%s/invoices?status=OPEN&limit=20", r.billingURL)
	return r.do(ctx, http.MethodGet, url, nil, nil)
}

func scenarioInvoiceRead(ctx context.Context, r *runner, worker int) (int, error) {
	id := r.anInvoice()
	if id == "" {
		return 0, fmt.Errorf("no invoice to read; run invoice-write first")
	}
	return r.do(ctx, http.MethodGet, r.billingURL+"/invoices/"+id, nil, nil)
}

// scenarioInvoiceWrite is the heaviest write on the critical path: it resolves
// every product against the stock service and stores the invoice with its items.
func scenarioInvoiceWrite(ctx context.Context, r *runner, worker int) (int, error) {
	lines := make([]map[string]any, 0, r.items)
	for index := range r.items {
		lines = append(lines, map[string]any{
			"product_id": r.aProduct(worker*31 + index),
			"quantity":   1,
		})
	}

	body, _ := json.Marshal(map[string]any{"items": lines})
	headers := map[string]string{"Idempotency-Key": r.uniqueKey()}

	status, err := r.do(ctx, http.MethodPost, r.billingURL+"/invoices", body, headers)
	return status, err
}

// scenarioBulkAdjust applies a delivery note: one transaction touching many
// products at once.
func scenarioBulkAdjust(ctx context.Context, r *runner, worker int) (int, error) {
	lines := make([]map[string]any, 0, r.batch)
	for index := range r.batch {
		lines = append(lines, map[string]any{
			"product_id": r.aProduct(worker*97 + index),
			"delta":      1,
		})
	}

	body, _ := json.Marshal(map[string]any{"items": lines})
	headers := map[string]string{"Idempotency-Key": r.uniqueKey()}
	return r.do(ctx, http.MethodPost, r.stockURL+"/products/adjustments", body, headers)
}

func scenarioBulkCreate(ctx context.Context, r *runner, worker int) (int, error) {
	lines := make([]map[string]any, 0, r.batch)
	for range r.batch {
		seed := r.nextSeed.Add(1)
		lines = append(lines, map[string]any{
			"code":        fmt.Sprintf("BULKLOAD-%d-%d", time.Now().UnixNano(), seed),
			"description": "Bulk load product",
			"balance":     10,
		})
	}

	body, _ := json.Marshal(map[string]any{"items": lines})
	headers := map[string]string{"Idempotency-Key": r.uniqueKey()}
	return r.do(ctx, http.MethodPost, r.stockURL+"/products/bulk", body, headers)
}

func (r *runner) aProduct(index int) string {
	return r.productIDs[index%len(r.productIDs)]
}

func (r *runner) anInvoice() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.invoiceIDs) == 0 {
		return ""
	}
	return r.invoiceIDs[rand.Intn(len(r.invoiceIDs))]
}

func (r *runner) uniqueKey() string {
	return fmt.Sprintf("load-%d-%d", time.Now().UnixNano(), r.nextSeed.Add(1))
}

// do performs one request and reports the status code it answered with.
func (r *runner) do(ctx context.Context, method, url string, body []byte, headers map[string]string) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+r.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := r.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	// The body has to be drained for the connection to be reused, and reusing
	// connections is the difference between measuring the service and
	// measuring TCP handshakes.
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

func (r *runner) signIn(ctx context.Context, email, password string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.identityURL+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := r.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}

	var answer struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return err
	}
	r.token = answer.AccessToken
	return nil
}

func (r *runner) seed(ctx context.Context, count int) error {
	const perCall = 100
	for created := 0; created < count; created += perCall {
		size := min(perCall, count-created)
		lines := make([]map[string]any, 0, size)
		for index := range size {
			lines = append(lines, map[string]any{
				"code":        fmt.Sprintf("LOAD-%d-%d", created+index, time.Now().UnixNano()%100000),
				"description": fmt.Sprintf("Load test product %d", created+index),
				"balance":     1_000_000,
			})
		}
		body, _ := json.Marshal(map[string]any{"items": lines})
		status, err := r.do(ctx, http.MethodPost, r.stockURL+"/products/bulk", body,
			map[string]string{"Idempotency-Key": r.uniqueKey()})
		if err != nil {
			return err
		}
		if status >= 400 {
			return fmt.Errorf("seeding answered %d", status)
		}
	}
	return nil
}

// loadProducts reads a few pages of the catalogue so the write scenarios have
// real product ids to work with.
func (r *runner) loadProducts(ctx context.Context) error {
	cursor := ""
	for range 10 {
		url := fmt.Sprintf("%s/products?limit=100", r.stockURL)
		if cursor != "" {
			url += "&cursor=" + cursor
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+r.token)

		response, err := r.client.Do(request)
		if err != nil {
			return err
		}
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		err = json.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if err != nil {
			return err
		}

		for _, item := range page.Items {
			r.productIDs = append(r.productIDs, item.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return nil
}

// run keeps `concurrency` requests in flight for the whole duration.
func (r *runner) run(ctx context.Context, work scenarioFunc, concurrency int, duration time.Duration) report {
	deadline, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		latencies []time.Duration
		statuses  = map[int]int{}
		failures  = map[string]int{}
	)

	started := time.Now()
	for worker := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, 1024)
			localStatus := map[int]int{}
			localFail := map[string]int{}

			for deadline.Err() == nil {
				begin := time.Now()
				status, err := work(deadline, r, worker)
				elapsed := time.Since(begin)

				if err != nil {
					if deadline.Err() != nil {
						break
					}
					localFail[err.Error()]++
					continue
				}
				local = append(local, elapsed)
				localStatus[status]++
			}

			mu.Lock()
			latencies = append(latencies, local...)
			for status, count := range localStatus {
				statuses[status] += count
			}
			for reason, count := range localFail {
				failures[reason] += count
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	return report{
		elapsed:   time.Since(started),
		latencies: latencies,
		statuses:  statuses,
		failures:  failures,
	}
}

type report struct {
	elapsed   time.Duration
	latencies []time.Duration
	statuses  map[int]int
	failures  map[string]int
}

func (r report) print() {
	if len(r.latencies) == 0 {
		fmt.Println("no successful requests")
		for reason, count := range r.failures {
			fmt.Printf("  %d x %s\n", count, reason)
		}
		return
	}

	sort.Slice(r.latencies, func(i, j int) bool { return r.latencies[i] < r.latencies[j] })

	total := len(r.latencies)
	ok := 0
	for status, count := range r.statuses {
		if status < 400 {
			ok += count
		}
	}

	fmt.Printf("requests=%d throughput=%.1f/s\n", total, float64(total)/r.elapsed.Seconds())
	fmt.Printf("latency p50=%s p90=%s p95=%s p99=%s max=%s\n",
		round(percentile(r.latencies, 50)),
		round(percentile(r.latencies, 90)),
		round(percentile(r.latencies, 95)),
		round(percentile(r.latencies, 99)),
		round(r.latencies[total-1]),
	)

	fmt.Print("status ")
	codes := make([]int, 0, len(r.statuses))
	for code := range r.statuses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		fmt.Printf("%d=%d ", code, r.statuses[code])
	}
	fmt.Println()

	if len(r.failures) > 0 {
		fmt.Println("transport failures:")
		for reason, count := range r.failures {
			fmt.Printf("  %d x %s\n", count, reason)
		}
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (p * len(sorted)) / 100
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func round(d time.Duration) time.Duration {
	if d > time.Millisecond {
		return d.Round(100 * time.Microsecond)
	}
	return d.Round(time.Microsecond)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loadgen: "+format+"\n", args...)
	os.Exit(1)
}
