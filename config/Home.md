# JIRAlert [![Build Status](https://travis-ci.org/tixu/jiralert.svg)](https://travis-ci.org/tixu/jiralert) [![Go Report Card](https://goreportcard.com/badge/github.com/tixu/jiralert)](https://goreportcard.com/report/github.com/tixu/jiralert) [![GoDoc](https://godoc.org/github.com/tixu/jiralert?status.svg)](https://godoc.org/github.com/tixu/jiralert)
[Prometheus Alertmanager](https://github.com/prometheus/alertmanager) webhook receiver for [JIRA](https://www.atlassian.com/software/jira).

## Overview

JIRAlert implements Alertmanager's webhook HTTP API and connects to one  JIRA instances to create highly configurable JIRA issues. One issue is created per distinct alert in the alert group but not closed when the alert is resolved. The expectation is that a human will look at the issue, take any necessary action, then close it.  If no human interaction is necessary then it should probably not alert in the first place.

If a corresponding JIRA issue already exists but is resolved, it is reopened. A JIRA transition must exist between the resolved state and the reopened state — as defined by `reopen_state` — or reopening will fail. Optionally a "won't fix" resolution — defined by `wont_fix_resolution` — may be defined: a JIRA issue with this resolution will not be reopened by JIRAlert.

## Usage

Get JIRAlert, either as a [packaged release](https://github.com/tixu/jiralert/releases) or build it yourself:

```
$ go get github.com/tixu/jiralert/cmd/jiralert
```

then run it from the command line:

```
$ jiralert
```

Use the `-help` flag to get help information.

```
$ jiralert -help
Usage of jiralert:
 -config string
        The JIRAlert configuration file (default "config")
  -dbfile string
        The local file (default "my.db")
  -jirapassword string
        The user's password accessing JIRA (default "jirapassword")
  -jiraurl string
        The Jira url (default "https://jira.smals.be")
  -jirauser string
        The user accessing JIRA (default "jirauser")
  -listen-address string
        The address to listen on for HTTP requests. (default ":9097")
```

## Testing

JIRAlert expects a JSON object from Alertmanager. The format of this JSON is described in the [Alertmanager documentation](https://prometheus.io/docs/alerting/configuration/#<webhook_config>) or, alternatively, in the [Alertmanager GoDoc](https://godoc.org/github.com/prometheus/alertmanager/template#Data).

To quickly test if JIRAlert is working you can run:

```bash
$ curl -H "Content-type: application/json" -X POST \
  -d '{"receiver": "jira-ab", "status": "firing", "alerts": [{"status": "firing", "labels": {"alertname": "TestAlert", "key": "value"} }], "groupLabels": {"alertname": "TestAlert"}}' \
  http://localhost:9097/alert
```

## Configuration

The configuration file is essentially a list of receivers matching 1-to-1 all Alertmanager receivers using JIRAlert; plus defaults (in the form of a partially defined receiver); and a pointer to the template file.

Each receiver must have a unique name (matching the Alertmanager receiver name), JIRA API access fields (URL, username and password), a handful of required issue fields (such as the JIRA project and issue summary), some optional issue fields (e.g. priority) and a `fields` map for other (standard or custom) JIRA fields. Most of these may use [Go templating](https://golang.org/pkg/text/template/) to generate the actual field values based on the contents of the Alertmanager notification. The exact same data structures and functions as those defined in the [Alertmanager template reference](https://prometheus.io/docs/alerting/notifications/) are available in JIRAlert.

## Alertmanager configuration

To enable Alertmanager to talk to JIRAlert you need to configure a webhook in Alertmanager. You can do that by adding a webhook receiver to your Alertmanager configuration. 

```yaml
receivers:
- name: 'jira-ab'
  webhook_configs:
  - url: 'http://localhost:9097/alert'
    # JIRAlert ignores resolved alerts, avoid unnecessary noise
    send_resolved: false
```

## Profiling

JIRAlert imports [`net/http/pprof`](https://golang.org/pkg/net/http/pprof/) to expose runtime profiling data on the `/debug/pprof` endpoint. For example, to use the pprof tool to look at a 30-second CPU profile:

```bash
go tool pprof http://localhost:9097/debug/pprof/profile
```

To enable mutex and block profiling (i.e. `/debug/pprof/mutex` and `/debug/pprof/block`) run JIRAlert with the `DEBUG` environment variable set:

```bash
env DEBUG=1 ./jiralert
```

## License

JIRAlert is licensed under the [MIT License](https://github.com/tixu/jiralert/blob/master/LICENSE).

