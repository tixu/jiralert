package jiralert

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"reflect"
	"strings"

	"github.com/andygrunwald/go-jira"
	log "github.com/sirupsen/logrus"
	"github.com/tixu/jiralert/alertmanager"
	"github.com/trivago/tgo/tcontainer"
	bolt "go.etcd.io/bbolt"
)

//github.com/andygrunwald/go-jira
// Receiver wraps a JIRA client corresponding to a specific Alertmanager receiver, with its configuration and templates.
type Receiver struct {
	conf   *ReceiverConfig
	tmpl   *Template
	client *jira.Client
	dbFile  string
}

type StatusNotify struct {
	Status int
	Err    error
}

type Notifier interface {
	Notify(data *alertmanager.Data) map[string]StatusNotify
}

// NewReceiver creates a Receiver using the provided configuration and template.
func NewReceiver(context context.Context, a *APIConfig, c *ReceiverConfig, t *Template, file string) (*Receiver, error) {
	client, err := jira.NewClient(http.DefaultClient, a.URL)
	if err != nil {
		return nil, err
	}
	
	
	client.Authentication.SetBasicAuth(a.User, string(a.Password))

	return &Receiver{conf: c, tmpl: t, client: client,dbFile: file}, nil
}
func(r  *Receiver) shutDown (){
	
}

// Notify implements the Notifier interface.
func (r *Receiver) Notify(context context.Context, data *alertmanager.Data) (map[string]StatusNotify, error) {
	
	var m map[string]StatusNotify = make(map[string]StatusNotify)
	project := r.tmpl.Execute(r.conf.Project, data)
	// check errors from r.tmpl.Execute()
	if r.tmpl.err != nil {
		return nil, r.tmpl.err
	}
	log.Infof("looping on the alerts from the group")
	// multipeErrors will be used to stock errors
	// occuring while iterating on alarms

	for _, alert := range data.Alerts {
		// Looks like an ALERT metric name, with spaces removed.
		issueLabel := toIssueLabel(alert.Labels)
		issue, err := r.getIssue(issueLabel, project)
		if err != nil {
			log.Warnf("got an error while searching %s", err)
			m[issueLabel] = StatusNotify{Status: http.StatusInternalServerError, Err: err}
			continue
		}

		if issue != nil {
			r.addComment(issue, r.tmpl.Execute(r.conf.Comment, alert))
			// The set of JIRA status categories is fixed, this is a safe check to make.
			if issue.Fields.Status.StatusCategory.Key != "done" {
				// Issue is in a "to do" or "in progress" state, all done here.
				log.Infof("Issue %s for %s is unresolved, nothing to do", issue.Key, issueLabel)
				// nothing to be done on this issues
				m[issueLabel] = StatusNotify{Status: http.StatusOK, Err: nil}
				continue
			}
			if r.conf.WontFixResolution != "" && issue.Fields.Resolution != nil &&
				issue.Fields.Resolution.Name == r.conf.WontFixResolution {
				// Issue is resolved as "Won't Fix" or equivalent, log a message just in case.
				log.Infof("Issue %s for %s is resolved as %q, not reopening", issue.Key, issueLabel, issue.Fields.Resolution.Name)
				// nothing to be done on this issues
				continue
			}
			log.Infof("Issue %s for %s was resolved, reopening", issue.Key, issueLabel)
			if err := r.reopen(issue.Key); err != nil {
				m[issueLabel] = StatusNotify{Status: http.StatusInternalServerError, Err: err}

			}
			m[issueLabel] = StatusNotify{Status: http.StatusOK, Err: nil}
			continue
		}

		log.Infof("No issue matching %s found, creating new issue", issueLabel)

		issue = &jira.Issue{
			Fields: &jira.IssueFields{
				Project:     jira.Project{Key: project},
				Type:        jira.IssueType{Name: r.tmpl.Execute(r.conf.IssueType, data)},
				Description: r.tmpl.Execute(r.conf.Description, alert),
				Summary:     r.tmpl.Execute(r.conf.Summary, alert),
				Labels: []string{
					issueLabel,
				},

				Unknowns: tcontainer.NewMarshalMap(),
			},
		}
		log.Printf("issue.field %+v", issue.Fields)
		if r.conf.Priority != "" {
			issue.Fields.Priority = &jira.Priority{Name: r.conf.Priority}
		}

		// Add Components
		if len(r.conf.Components) > 0 {
			issue.Fields.Components = make([]*jira.Component, 0, len(r.conf.Components))
			for _, component := range r.conf.Components {
				issue.Fields.Components = append(issue.Fields.Components, &jira.Component{Name: component})
			}
		}

		// Add Labels
		if r.conf.AddGroupLabels {
			for k, v := range data.GroupLabels {
				issue.Fields.Labels = append(issue.Fields.Labels, fmt.Sprintf("%s=%q", k, v))
			}
		}

		// check errors from r.tmpl.Execute()
		if r.tmpl.err != nil {
			m[issueLabel] = StatusNotify{Status: http.StatusInternalServerError, Err: r.tmpl.err}
			continue
		}
		issue, err = r.create(issue)
		log.Infof("issue %+v", issue)
		if err != nil {
			m[issueLabel] = StatusNotify{Status: http.StatusInternalServerError, Err: err}
			continue
		}
		m[issueLabel] = StatusNotify{Status: http.StatusOK, Err: nil}
		log.Infof("Issue created: key=%s ID=%s", issue.Key, issue.ID)

	}

	return m, nil

}

// deepCopyWithTemplate returns a deep copy of a map/slice/array/string/int/bool or combination thereof, executing the
// provided template (with the provided data) on all string keys or values. All maps are connverted to
// map[string]interface{}, with all non-string keys discarded.
func deepCopyWithTemplate(value interface{}, tmpl *Template, data interface{}) interface{} {
	if value == nil {
		return value
	}

	valueMeta := reflect.ValueOf(value)
	switch valueMeta.Kind() {

	case reflect.String:
		return tmpl.Execute(value.(string), data)

	case reflect.Array, reflect.Slice:
		arrayLen := valueMeta.Len()
		converted := make([]interface{}, arrayLen)
		for i := 0; i < arrayLen; i++ {
			converted[i] = deepCopyWithTemplate(valueMeta.Index(i).Interface(), tmpl, data)
		}
		return converted

	case reflect.Map:
		keys := valueMeta.MapKeys()
		converted := make(map[string]interface{}, len(keys))

		for _, keyMeta := range keys {
			strKey, isString := keyMeta.Interface().(string)
			if !isString {
				continue
			}
			strKey = tmpl.Execute(strKey, data)
			converted[strKey] = deepCopyWithTemplate(valueMeta.MapIndex(keyMeta).Interface(), tmpl, data)
		}
		return converted

	default:
		return value
	}
}

// toIssueLabel returns the group labels in the form of an ALERT metric name, with all spaces removed.
func toIssueLabel(groupLabels alertmanager.KV) string {
	buf := bytes.NewBufferString("ALERT{")
	for _, p := range groupLabels.SortedPairs() {
		buf.WriteString(p.Name)
		buf.WriteString(fmt.Sprintf("=%q,", p.Value))
	}
	buf.Truncate(buf.Len() - 1)
	buf.WriteString("}")
	return strings.Replace(buf.String(), " ", "", -1)
}

func (r *Receiver) search(project, issueLabel string) (*jira.Issue, error) {

	query := fmt.Sprintf("project=%s and labels=%q order by key", project, issueLabel)
	options := &jira.SearchOptions{
		Fields:     []string{"summary", "status", "resolution"},
		MaxResults: 50,
	}
	log.Infof("search: query=%v options=%+v", query, options)
	issues, resp, err := r.client.Issue.Search(query, options)
	if err != nil {
		err := handleJiraError("Issue.Search", resp, err)
		return nil, err
	}
	if len(issues) > 0 {
		if len(issues) > 1 {
			// Swallow it, but log an error.
			log.Errorf("More than one issue matched %s, will only update first: %+v", query, issues)
		}
		log.Infof("  found: %+v", issues[0])
		return &issues[0], nil
	}
	log.Infof("  no results")
	return nil, nil
}
func (r *Receiver) addComment(issue *jira.Issue, commentstring string) error {
	comment := &jira.Comment{Body: commentstring}
	comment, _, err := r.client.Issue.AddComment(issue.ID, comment)
	return err

}
func (r *Receiver) reopen(issueKey string) error {
	transitions, resp, err := r.client.Issue.GetTransitions(issueKey)
	if err != nil {
		return handleJiraError("Issue.GetTransitions", resp, err)
	}
	for _, t := range transitions {
		if t.Name == r.conf.ReopenState {
			log.Infof("reopen: issueKey=%v transitionID=%v", issueKey, t.ID)
			resp, err = r.client.Issue.DoTransition(issueKey, t.ID)
			if err != nil {
				return handleJiraError("Issue.DoTransition", resp, err)
			}
			log.Infof("  done")
			return nil
		}
	}
	return fmt.Errorf("JIRA state %q does not exist or no transition possible for %s", r.conf.ReopenState, issueKey)
}

func (r *Receiver) create(issue *jira.Issue) (*jira.Issue, error) {
	log.Infof("create: issue=%+v", *issue)
	issue, resp, err := r.client.Issue.Create(issue)
	if err != nil {
		return nil, handleJiraError("Issue.Create", resp, err)
	}

	log.Infof("  done: key=%s ID=%s", issue.Key, issue.ID)
	return issue, nil
}

func (r *Receiver) getIssue(issueLabel, project string) (*jira.Issue, error) {
	db, err := bolt.Open(r.dbFile, 0600, nil)
	
	
	
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	
	log.Infof("getting   issue with label : %s", issueLabel)
	var id string
	
	err = db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte("JIRA"))
		bs := bk.Get([]byte(issueLabel))
		if bs == nil {
			return fmt.Errorf("issue not found locally")
		}
		id = string(bs)
		log.Infof("local ID is %s", id)
		return nil
	})

	if len(id) == 0 {
		// we did not find anything
		issue, err := r.search(project, issueLabel)
		if err != nil {
			log.Warnf("got an error while searching %s", err)
			return nil, err
		}
		if issue == nil {
			return nil, nil
		
			}	// we found something
		err = db.Update(func(tx *bolt.Tx) error {
			bk := tx.Bucket([]byte("JIRA"))
			return bk.Put([]byte(issueLabel), []byte(issue.ID))

		})
		//we return the issue after updating the db
		return issue, nil
	}

	issue, _, err := r.client.Issue.Get(id, nil)
	if err != nil {
		log.Infof("got an error while getting the issue by id %s", err )
		issue, err := r.search(project, issueLabel)
		if err != nil {
			log.Warnf("got an error while searching %s", err)
			return nil, err
		}
		if issue != nil {// we found something
		err = db.Update(func(tx *bolt.Tx) error {
			bk, err := tx.CreateBucketIfNotExists([]byte("JIRA"))
			if err != nil {
				return err
			}
			return bk.Put([]byte(issueLabel), []byte(issue.ID))
		})
	}
		//we return the issue after updating the db
		return issue, nil
	}
	return issue, nil

}

func handleJiraError(api string, resp *jira.Response, err error) error {
	if resp == nil || resp.Request == nil {
		log.Infof("handleJiraError: api=%s, err=%s", api, err)
	} else {
		log.Infof("handleJiraError: api=%s, url=%s, err=%s", api, resp.Request.URL, err)
	}

	if resp != nil && resp.StatusCode/100 != 2 {
		body, _ := ioutil.ReadAll(resp.Body)
		requestDump, err := httputil.DumpRequest(resp.Request, false)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println(string(requestDump))
		// go-jira error message is not particularly helpful, replace it
		return fmt.Errorf("JIRA request %s returned status %s, body %q", resp.Request.URL, resp.Status, string(body))
	}
	return fmt.Errorf("JIRA request %s failed: %s", api, err)
}
