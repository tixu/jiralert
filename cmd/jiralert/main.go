package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "net/http/pprof"

	log "github.com/sirupsen/logrus"

	"github.com/tixu/jiralert"
	"github.com/tixu/jiralert/alertmanager"
	bolt "go.etcd.io/bbolt"
	"go.opencensus.io/exporter/prometheus"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

const (
	unknownReceiver = "<unknown>"
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config", "The JIRAlert configuration file")
	dbFileName    string
	logFileName   string
	jirauser      = flag.String("jirauser", "jirauser", "The user accessing JIRA")
	jirapassword  = flag.String("jirapassword", "jirapassword", "The user's password accessing JIRA")
	jiraurl       = flag.String("jiraurl", "https://jira.smals.be", "The Jira url")
	logLevel      = flag.String("loglevel", "PROD", "log level either PROD or DEV")
	dataDir       = flag.String("datadir", ".", "location of temporaty file")
	startDate     string

	// Version is the build version, set by make to latest git tag/hash via `-ldflags "-X main.Version=$(VERSION)"`.
	Version = "<local build>"
	// BuildDate is the Build date
	BuildDate = "<build_date>"
	// Hash is the git hash
	Hash = "<hash>"

	cfg  = &jiralert.Config{}
	tmpl = &jiralert.Template{}

	// Metrics related variable.
	MGroupIn      = stats.Int64("jira/group_in", "The number of jira group received", "1")
	MAlarmIn      = stats.Int64("jira/alarm_in", "The number of alarms we received", "1")
	MConfigReload = stats.Int64("jira/config", "the number of config reload", "1")

	receiverKey, _ = tag.NewKey("receiver")
	alarmKey, _    = tag.NewKey("alert")
	statusKey, _   = tag.NewKey("status")

	GroupCountView = &view.View{
		Name:        "jiralert/group",
		Measure:     MGroupIn,
		TagKeys:     []tag.Key{receiverKey},
		Description: "The number of jira group received",
		Aggregation: view.Sum(),
	}
	AlarmsCountView = &view.View{
		Name:        "jiralert/alarm",
		Measure:     MAlarmIn,
		TagKeys:     []tag.Key{receiverKey, alarmKey, statusKey},
		Description: "The number of alarms received",
		Aggregation: view.Count(),
	}
	ReloadsCountView = &view.View{
		Name:        "jiralert/reload",
		Measure:     MConfigReload,
		Description: "The number of reload request received",
		Aggregation: view.Count(),
	}
)

func init() {
	startDate = time.Now().Format("2006-01-02 15:04:05")
	flag.Parse()
	logFileName = *dataDir + "/logfile.log"
	dbFileName = *dataDir + "/jiralert.db"
	// Create the log file if doesn't exist. And append to it if it already exists.
	logFile, err := os.OpenFile(logFileName, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	mw := io.MultiWriter(os.Stdout, logFile)
	Formatter := new(log.TextFormatter)
	// You can change the Timestamp format. But you have to use the same date and time.
	// "2006-02-02 15:04:06" Works. If you change any digit, it won't work
	// ie "Mon Jan 2 15:04:05 MST 2006" is the reference time. You can't change it
	Formatter.TimestampFormat = "02-01-2006 15:04:05"
	Formatter.FullTimestamp = true
	log.SetFormatter(Formatter)
	switch *logLevel {
	case "PROD":
		log.SetLevel(log.ErrorLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}

	if err != nil {
		// Cannot open log file. Logging to stderr
		fmt.Println(err)
	} else {
		log.SetOutput(mw)
	}

}
func main() {

	exporter, err := prometheus.NewExporter(prometheus.Options{})
	if err != nil {
		log.Fatal(err)
	}
	view.RegisterExporter(exporter)
	if err := view.Register(GroupCountView, AlarmsCountView, ReloadsCountView); err != nil {
		log.Fatalf("Failed to register views: %v", err)
	}
	// Set reporting period to report data at every second.
	view.SetReportingPeriod(10 * time.Second)

	jiraEndpoint := jiralert.APIConfig{URL: *jiraurl, User: *jirauser, Password: *jirapassword}
	log.Infof("Starting JIRAlert version %s hash %s date %s", Version, Hash, BuildDate)
	if err = initDB(dbFileName); err != nil {
		panic(err)
	}

	err = cfg.ReadConfiguration(*configFile)
	if err != nil {
		log.Fatalf("Error loading configuration: %s", err)
	}

	tmpl, err = jiralert.LoadTemplate(cfg.Template)
	if err != nil {
		log.Fatalf("Error loading templates from %s: %s", cfg.Template, err)
	}
	http.HandleFunc("/reload", func(w http.ResponseWriter, req *http.Request) {
		log.Infof("reloading config....")
		defer stats.Record(context.Background(), MConfigReload.M(1))

		err = cfg.ReadConfiguration(*configFile)
		if err != nil {
			log.Fatalf("Error loading configuration: %s", err)
			errorHandler(w, 500, err, "bad config", nil)
			return
		}

		tmpl, err = jiralert.LoadTemplate(cfg.Template)
		if err != nil {
			log.Fatalf("Error loading templates from %s: %s", cfg.Template, err)
			errorHandler(w, 500, err, "bad config", nil)
			return
		}

		switch req.Method {
		case http.MethodGet:
			// Serve the resource.
			http.Redirect(w, req, "/config", 302)

		case http.MethodPost:
			// Create a new record.
		case http.MethodPut:
			// Update an existing record.
		case http.MethodDelete:
			// Remove the record.
		default:
			// Give an error message.
		}

	})
	http.HandleFunc("/alert", func(w http.ResponseWriter, req *http.Request) {
		log.Infof("Handling /alert webhook request")
		// https://godoc.org/github.com/prometheus/alertmanager/template#Data
		data := alertmanager.Data{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			errorHandler(w, http.StatusBadRequest, err, unknownReceiver, &data)
			return
		}
		defer req.Body.Close()
		ctx, err := tag.New(context.Background(), tag.Insert(receiverKey, data.Receiver))
		if err != nil {
			log.Fatal(err)
		}
		defer stats.Record(ctx, MGroupIn.M(1))
		conf := cfg.ReceiverByName(data.Receiver)
		if conf == nil {
			tag.Insert(statusKey, strconv.Itoa(http.StatusNotFound))
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
			r, err := jiralert.NewReceiver(ctx, &jiraEndpoint, conf, tmpl, dbFileName)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}
			log.Info("able to create receiver")
			m, err := r.Notify(ctx, &data)
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
				alertctx, _ := tag.New(ctx, tag.Insert(alarmKey, k), tag.Insert(statusKey, strconv.Itoa((m[k].Status))))
				stats.Record(alertctx, MAlarmIn.M(1))

				if responseStatus == 0 {
					responseStatus = m[k].Status
				}
				if responseStatus != m[k].Status {
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
	http.HandleFunc("/config", ConfigHandlerFunc(cfg))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "OK", http.StatusOK) })
	http.HandleFunc("/logs", LogsHandlerFunc())
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

func initDB(dbFile string) error {

	db, err := bolt.Open(dbFile, 0666, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("JIRA"))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		return nil
	})
	return nil

}
