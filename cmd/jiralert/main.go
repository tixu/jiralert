package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/tixu/jiralert"
	"github.com/tixu/jiralert/alertmanager"
)

const (
	unknownReceiver = "<unknown>"
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config/jiralert.yml", "The JIRAlert configuration file")
	jirauser      = flag.String("jirauser", "jirauser", "The user accessing JIRA")
	jirapassword  = flag.String("jirapassword", "jirapassword", "The user's password accessing JIRA")
	jiraurl       = flag.String("jiraurl", "https://jira.smals.be", "The Jira url")
	// Version is the build version, set by make to latest git tag/hash via `-ldflags "-X main.Version=$(VERSION)"`.
	Version = "<local build>"
	// BuildDate is the Build date
	BuildDate = "<build_date>"
	// Hash is the git hash
	Hash = "<hash>"
)

func main() {

	flag.Parse()
	jiraEndpoint := jiralert.APIConfig{URL: *jiraurl, User: *jirauser, Password: *jirapassword}
	log.Infof("Starting JIRAlert version %s hash %s date %s", Version, Hash, BuildDate)

	config, err := jiralert.ReadConfiguration("config")
	if err != nil {
		log.Fatalf("Error loading configuration: %s", err)
	}

	tmpl, err := jiralert.LoadTemplate(config.Template)
	if err != nil {
		log.Fatalf("Error loading templates from %s: %s", config.Template, err)
	}

	http.HandleFunc("/alert", func(w http.ResponseWriter, req *http.Request) {
		log.Infof("Handling /alert webhook request")
		defer req.Body.Close()

		// https://godoc.org/github.com/prometheus/alertmanager/template#Data
		data := alertmanager.Data{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			errorHandler(w, http.StatusBadRequest, err, unknownReceiver, &data)
			return
		}

		conf := config.ReceiverByName(data.Receiver)
		if conf == nil {
			errorHandler(w, http.StatusNotFound, fmt.Errorf("Receiver missing: %s", data.Receiver), unknownReceiver, &data)
			return
		}
		log.Infof("Matched receiver: %q", conf.Name)

		// Filter out resolved alerts, not interested in them.
		alerts := data.Alerts.Firing()
		if len(alerts) < len(data.Alerts) {
			log.Warningf("Please set \"send_resolved: false\" on receiver %s in the Alertmanager config", conf.Name)
			data.Alerts = alerts
		}

		if len(data.Alerts) > 0 {
			r, err := jiralert.NewReceiver(&jiraEndpoint, conf, tmpl)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}
			if err := r.Notify(&data); err != nil {
				istemporary := func(err error) bool {
					te, ok := err.(jiralert.Temporary)
					return ok && te.Temporary()
				}
				var status int
				if istemporary(err) {
					status = http.StatusServiceUnavailable
				} else {
					status = http.StatusInternalServerError
				}
				errorHandler(w, status, err, conf.Name, &data)
				return
			}
		}

		requestTotal.WithLabelValues(conf.Name, "200").Inc()
	})

	http.HandleFunc("/", HomeHandlerFunc())
	http.HandleFunc("/config", ConfigHandlerFunc(config))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "OK", http.StatusOK) })
	http.Handle("/metrics", promhttp.Handler())

	if os.Getenv("PORT") != "" {
		*listenAddress = ":" + os.Getenv("PORT")
	}

	log.Infof("Listening on %s", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func errorHandler(w http.ResponseWriter, status int, err error, receiver string, data *alertmanager.Data) {
	w.WriteHeader(status)

	response := struct {
		Error   bool
		Status  int
		Message string
	}{
		true,
		status,
		err.Error(),
	}
	// JSON response
	bytes, _ := json.Marshal(response)
	json := string(bytes[:])
	fmt.Fprint(w, json)

	log.Errorf("%d %s: err=%s receiver=%q groupLabels=%+v", status, http.StatusText(status), err, receiver, data.GroupLabels)
	requestTotal.WithLabelValues(receiver, strconv.FormatInt(int64(status), 10)).Inc()
}
