package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "net/http/pprof"


	log "github.com/sirupsen/logrus"
	"github.com/tixu/jiralert"
	"github.com/tixu/jiralert/alertmanager"
	"go.opencensus.io/exporter/prometheus"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	
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

	MGroupIn       = stats.Int64("jira/group_in", "The number of jira group received", "1")
	MAlarmIn       = stats.Int64("jira/alarm_in", "The number of alarms we received","1")
	
	receiverKey, _ = tag.NewKey("receiver")
	alarmKey, _ = tag.NewKey("alert")
	statusKey,_ = tag.NewKey("status")
	
	GroupCountView = &view.View{
		Name:        "jiralert/group",
		Measure:     MGroupIn,
		TagKeys: []tag.Key{receiverKey},
		Description: "The number of jira group received",
		Aggregation: view.Sum(),
	}
	AlarmsCountView = &view.View{
		Name:        "jiralert/alarm",
		Measure:     MAlarmIn,
		TagKeys: []tag.Key{receiverKey,alarmKey,statusKey},
		Description: "The number of alarms received",
		Aggregation: view.Count(),
	}
)

func main() {
	
	exporter, err := prometheus.NewExporter(prometheus.Options{})
	if err != nil {
		log.Fatal(err)
	}
	view.RegisterExporter(exporter)
	if err := view.Register(GroupCountView,AlarmsCountView); err != nil {
		log.Fatalf("Failed to register views: %v", err)
	}
	// Set reporting period to report data at every second.
	view.SetReportingPeriod(1 * time.Second)
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

	http.HandleFunc("/alert",func(w http.ResponseWriter, req *http.Request) {
		
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
		ctx, err := tag.New(context.Background(), tag.Insert(receiverKey,data.Receiver)) 
		if (err !=nil){
			log.Fatal(err)
		}
		//stats.Record(ctx,MGroupIn.M(1))
		defer stats.Record(ctx,MGroupIn.M(1))
		alerts := data.Alerts.Firing()
		if len(alerts) < len(data.Alerts) {
			log.Warningf("Please set \"send_resolved: false\" on receiver %s in the Alertmanager config", conf.Name)
			data.Alerts = alerts
		}

		if len(data.Alerts) > 0 {
			r, err := jiralert.NewReceiver(ctx, &jiraEndpoint, conf, tmpl)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}
			log.Info("able to create receiver")
			m, err := r.Notify(ctx,&data)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}
			log.Infof("responses %+v", m)
			statusJson, err := json.Marshal(m)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}

			responseStatus := 0
			for k := range m {
				alertctx, _ := tag.New(ctx, tag.Insert(alarmKey, k),tag.Insert(statusKey,strconv.Itoa((m[k].Status))))
				stats.Record(alertctx,MAlarmIn.M(1))
				
				if responseStatus == 0 {
					responseStatus = m[k].Status
				}
				if responseStatus != m[k].Status{
					responseStatus = http.StatusMultiStatus
					break
				}
				

			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(responseStatus)
			w.Write(statusJson)
		}
	})

	http.HandleFunc("/", HomeHandlerFunc())
	http.HandleFunc("/config", ConfigHandlerFunc(config))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "OK", http.StatusOK) })
	http.Handle("/metrics", exporter)

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
