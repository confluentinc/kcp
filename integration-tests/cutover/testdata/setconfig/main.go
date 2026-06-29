// Test fixture helper: PUTs a value to the cluster link's per-config endpoint
// inside the cutover e2e environment. Used by TestCutoverE2E_PauseOffsetSync_*
// to flip consumer.offset.sync.enable between true/false without depending on
// curl (busybox wget cannot do PUT).
//
// Usage:
//
//	setconfig --url http://host:8090 --cluster lkc-x --link e2e-link \
//	          --name consumer.offset.sync.enable --value false
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	var (
		endpoint   = flag.String("url", "", "REST endpoint base URL (e.g. http://destination-kafka.confluent.svc.cluster.local:8090) (required)")
		clusterID  = flag.String("cluster", "", "destination cluster ID (required)")
		linkName   = flag.String("link", "", "cluster link name (required)")
		configName = flag.String("name", "", "config key (required)")
		configVal  = flag.String("value", "", "config value (required for SET)")
		op         = flag.String("op", "SET", "operation: SET (PUT) or DELETE")
		apiKey     = flag.String("api-key", "", "REST API key (optional, basic auth)")
		apiSecret  = flag.String("api-secret", "", "REST API secret (optional, basic auth)")
	)
	flag.Parse()

	if *endpoint == "" || *clusterID == "" || *linkName == "" || *configName == "" {
		log.Fatal("--url, --cluster, --link, --name are required")
	}

	path := fmt.Sprintf("/kafka/v3/clusters/%s/links/%s/configs/%s",
		url.PathEscape(*clusterID), url.PathEscape(*linkName), url.PathEscape(*configName))

	client := &http.Client{Timeout: 30 * time.Second}

	var req *http.Request
	var err error
	switch *op {
	case "SET":
		body, _ := json.Marshal(map[string]string{"value": *configVal})
		req, err = http.NewRequest(http.MethodPut, *endpoint+path, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	case "DELETE":
		req, err = http.NewRequest(http.MethodDelete, *endpoint+path, nil)
	default:
		log.Fatalf("invalid --op %q (want SET or DELETE)", *op)
	}
	if err != nil {
		log.Fatalf("build request: %v", err)
	}
	if *apiKey != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(*apiKey+":"+*apiSecret)))
	}

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	respBody, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		log.Fatalf("unexpected status %d: %s", res.StatusCode, string(respBody))
	}
	fmt.Fprintf(os.Stderr, "setconfig: %s %s = %s — status %d\n", *op, *configName, *configVal, res.StatusCode)
}
