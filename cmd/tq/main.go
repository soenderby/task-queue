package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	tq "github.com/soenderby/task-queue"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	switch args[0] {
	case "help":
		printUsage(os.Stdout)
		return nil
	case "init":
		return runInit(args[1:])
	case "create":
		return runCreate(args[1:])
	case "show":
		return runShow(args[1:])
	case "list":
		return runList(args[1:])
	case "ready":
		return runReady(args[1:])
	case "claim":
		return runClaim(args[1:])
	case "update":
		return runUpdate(args[1:])
	case "close":
		return runClose(args[1:])
	case "reopen":
		return runReopen(args[1:])
	case "comment":
		return runComment(args[1:])
	case "dep":
		return runDep(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInit(args []string) error {
	fs := newFlagSet("init")
	var dir string
	var prefix string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.StringVar(&prefix, "prefix", "", "id prefix")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetDir := dir
	if targetDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		targetDir = wd
	}

	if prefix == "" {
		prefix = strings.ToLower(filepath.Base(targetDir))
	}

	if err := tq.Init(targetDir, prefix); err != nil {
		if errors.Is(err, tq.ErrInvalidPrefix) {
			return fmt.Errorf("%w (pass --prefix explicitly)", err)
		}
		return err
	}

	fmt.Println("initialized .tq")
	return nil
}

func runCreate(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("create requires TITLE")
	}
	title := args[0]

	fs := newFlagSet("create")
	var dir string
	var description string
	var issueType string
	var assignee string
	var createdBy string
	var jsonOut bool
	var labels stringList
	var priority optInt

	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.StringVar(&description, "description", "", "description")
	fs.StringVar(&description, "d", "", "description")
	fs.Var(&priority, "priority", "priority 0-4")
	fs.Var(&priority, "p", "priority 0-4")
	fs.StringVar(&issueType, "type", "", "issue type")
	fs.StringVar(&issueType, "t", "", "issue type")
	fs.Var(&labels, "label", "label (repeatable)")
	fs.Var(&labels, "l", "label (repeatable)")
	fs.StringVar(&assignee, "assignee", "", "assignee")
	fs.StringVar(&assignee, "a", "", "assignee")
	fs.StringVar(&createdBy, "created-by", "", "created-by")
	fs.BoolVar(&jsonOut, "json", false, "json output")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	opts := tq.CreateOpts{
		Title:       title,
		Description: description,
		Type:        issueType,
		Labels:      labels,
		Assignee:    assignee,
		CreatedBy:   createdBy,
	}
	if priority.set {
		opts.Priority = &priority.v
	}

	issue, err := store.Create(opts)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(issue)
	}
	fmt.Println(issue.ID)
	return nil
}

func runShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("show requires ID")
	}
	id := args[0]

	fs := newFlagSet("show")
	var dir string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issue, err := store.Show(id)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(issue)
	}
	printIssue(issue)
	return nil
}

func runList(args []string) error {
	fs := newFlagSet("list")
	var dir string
	var jsonOut bool
	var statuses stringList
	var labels stringList
	var assignee string
	var priority optInt

	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	fs.Var(&statuses, "status", "status filter (repeatable)")
	fs.Var(&labels, "label", "label filter (repeatable)")
	fs.StringVar(&assignee, "assignee", "", "assignee filter")
	fs.Var(&priority, "priority", "priority filter")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	filter := tq.ListFilter{Status: statuses, Label: labels, Assignee: assignee}
	if priority.set {
		filter.Priority = &priority.v
	}

	issues, err := store.List(filter)
	if err != nil {
		return err
	}
	warnMalformed(store)

	if jsonOut {
		return printJSON(issues)
	}
	printIssueList(issues)
	return nil
}

func runReady(args []string) error {
	fs := newFlagSet("ready")
	var dir string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issues, err := store.Ready()
	if err != nil {
		return err
	}
	warnMalformed(store)

	if jsonOut {
		return printJSON(issues)
	}
	printIssueList(issues)
	return nil
}

func runClaim(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("claim requires ID")
	}
	id := args[0]

	fs := newFlagSet("claim")
	var dir string
	var actor string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.StringVar(&actor, "actor", "", "actor")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if actor == "" {
		return fmt.Errorf("claim requires --actor")
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issue, err := store.Claim(id, actor)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(issue)
	}
	fmt.Println(issue.ID)
	return nil
}

func runUpdate(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("update requires ID")
	}
	id := args[0]

	fs := newFlagSet("update")
	var dir string
	var jsonOut bool
	var status optString
	var priority optInt
	var assignee optString
	var description optString
	var addLabels stringList
	var removeLabels stringList

	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	fs.Var(&status, "status", "status")
	fs.Var(&priority, "priority", "priority")
	fs.Var(&assignee, "assignee", "assignee")
	fs.Var(&description, "description", "description")
	fs.Var(&addLabels, "add-label", "label to add")
	fs.Var(&removeLabels, "remove-label", "label to remove")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	opts := tq.UpdateOpts{AddLabels: addLabels, RemoveLabels: removeLabels}
	if status.set {
		opts.Status = &status.v
	}
	if priority.set {
		opts.Priority = &priority.v
	}
	if assignee.set {
		opts.Assignee = &assignee.v
	}
	if description.set {
		opts.Description = &description.v
	}

	issue, err := store.Update(id, opts)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(issue)
	}
	fmt.Println(issue.ID)
	return nil
}

func runClose(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("close requires ID")
	}
	id := args[0]

	fs := newFlagSet("close")
	var dir string
	var reason string
	var actor string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.StringVar(&reason, "reason", "", "close reason")
	fs.StringVar(&actor, "actor", "", "actor")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issue, err := store.Close(id, reason, actor)
	if err != nil {
		return err
	}
	fmt.Println(issue.ID)
	return nil
}

func runReopen(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("reopen requires ID")
	}
	id := args[0]

	fs := newFlagSet("reopen")
	var dir string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issue, err := store.Reopen(id)
	if err != nil {
		return err
	}
	fmt.Println(issue.ID)
	return nil
}

func runComment(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("comment requires ID and TEXT")
	}
	id := args[0]
	text := args[1]

	fs := newFlagSet("comment")
	var dir string
	var author string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.StringVar(&author, "author", "", "author")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	issue, err := store.Comment(id, author, text)
	if err != nil {
		return err
	}
	fmt.Println(issue.ID)
	return nil
}

func runDep(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("dep requires subcommand: add|remove|list")
	}
	switch args[0] {
	case "add":
		return runDepAdd(args[1:])
	case "remove":
		return runDepRemove(args[1:])
	case "list":
		return runDepList(args[1:])
	default:
		return fmt.Errorf("unknown dep subcommand %q", args[0])
	}
}

func runDepAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("dep add requires ID and BLOCKED_BY_ID")
	}
	id := args[0]
	blockedBy := args[1]

	fs := newFlagSet("dep add")
	var dir string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	if err := store.DepAdd(id, blockedBy); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

func runDepRemove(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("dep remove requires ID and BLOCKED_BY_ID")
	}
	id := args[0]
	blockedBy := args[1]

	fs := newFlagSet("dep remove")
	var dir string
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	if err := store.DepRemove(id, blockedBy); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

func runDepList(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("dep list requires ID")
	}
	id := args[0]

	fs := newFlagSet("dep list")
	var dir string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	graph, err := store.DepList(id)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(graph)
	}
	printDepGraph(graph)
	return nil
}

func runDoctor(args []string) error {
	fs := newFlagSet("doctor")
	var dir string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "", "workspace root directory")
	fs.BoolVar(&jsonOut, "json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := openStore(discoverWorkspace(dir))
	if err != nil {
		return err
	}

	report, err := store.Doctor()
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(report)
	}
	printDoctor(report)
	if !report.OK {
		return fmt.Errorf("doctor reported failures")
	}
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "tq commands:")
	fmt.Fprintln(w, "  init [--prefix PREFIX] [--dir PATH]")
	fmt.Fprintln(w, "  create TITLE [options]")
	fmt.Fprintln(w, "  show ID [--json]")
	fmt.Fprintln(w, "  list [options] [--json]")
	fmt.Fprintln(w, "  ready [--json]")
	fmt.Fprintln(w, "  claim ID --actor NAME [--json]")
	fmt.Fprintln(w, "  update ID [options]")
	fmt.Fprintln(w, "  close ID [--reason TEXT] [--actor NAME]")
	fmt.Fprintln(w, "  reopen ID")
	fmt.Fprintln(w, "  comment ID TEXT [--author NAME]")
	fmt.Fprintln(w, "  dep add ID BLOCKED_BY_ID")
	fmt.Fprintln(w, "  dep remove ID BLOCKED_BY_ID")
	fmt.Fprintln(w, "  dep list ID [--json]")
	fmt.Fprintln(w, "  doctor [--json]")
}

func openStore(root string, err error) (*tq.Store, error) {
	if err != nil {
		return nil, err
	}
	return tq.Open(root)
}

func discoverWorkspace(dirFlag string) (string, error) {
	if dirFlag != "" {
		return dirFlag, nil
	}
	if env := os.Getenv("TQ_DIR"); env != "" {
		return env, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	curr := wd
	for {
		st, err := os.Stat(filepath.Join(curr, ".tq"))
		if err == nil && st.IsDir() {
			return curr, nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", tq.ErrNotInitialized
}

func warnMalformed(store *tq.Store) {
	report, err := store.Doctor()
	if err != nil {
		return
	}
	for _, check := range report.Checks {
		if check.ID == "issue_json_valid" && check.Status == "fail" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", check.Message)
			return
		}
	}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(v)
}

func printIssueList(issues []*tq.Issue) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, issue := range issues {
		typeValue := issue.Type
		if typeValue == "" {
			typeValue = tq.DefaultType
		}
		fmt.Fprintf(tw, "%s\tP%d\t%s\t%s\n", issue.ID, issue.Priority, typeValue, issue.Title)
	}
	_ = tw.Flush()
}

func printIssue(issue *tq.Issue) {
	assignee := issue.Assignee
	if assignee == "" {
		assignee = "(none)"
	}
	labels := "(none)"
	if len(issue.Labels) > 0 {
		labels = strings.Join(issue.Labels, ",")
	}
	fmt.Printf("%s  %s\n\n", issue.ID, issue.Title)
	fmt.Printf("  Status:      %s\n", issue.Status)
	fmt.Printf("  Priority:    %d\n", issue.Priority)
	fmt.Printf("  Type:        %s\n", issue.Type)
	fmt.Printf("  Assignee:    %s\n", assignee)
	fmt.Printf("  Labels:      %s\n", labels)
	fmt.Printf("  Created:     %s", issue.CreatedAt)
	if issue.CreatedBy != "" {
		fmt.Printf(" by %s", issue.CreatedBy)
	}
	fmt.Println()
	if issue.Description != "" {
		fmt.Printf("\n  %s\n", issue.Description)
	}
	if len(issue.Comments) > 0 {
		fmt.Println("\n  Comments:")
		for _, c := range issue.Comments {
			fmt.Printf("    [%s %s] %s\n", c.CreatedAt, c.Author, c.Text)
		}
	}
}

func printDepGraph(graph *tq.DepGraph) {
	fmt.Println("Blocked by:")
	for _, issue := range graph.BlockedBy {
		fmt.Printf("  - %s\n", issue.ID)
	}
	fmt.Println("Blocks:")
	for _, issue := range graph.Blocks {
		fmt.Printf("  - %s\n", issue.ID)
	}
}

func printDoctor(report *tq.DoctorReport) {
	for _, check := range report.Checks {
		if check.Message != "" {
			fmt.Printf("[%s] %s: %s\n", check.Status, check.Name, check.Message)
		} else {
			fmt.Printf("[%s] %s\n", check.Status, check.Name)
		}
	}
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type optInt struct {
	set bool
	v   int
}

func (o *optInt) String() string {
	if !o.set {
		return ""
	}
	return strconv.Itoa(o.v)
}

func (o *optInt) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return err
	}
	o.v = n
	o.set = true
	return nil
}

type optString struct {
	set bool
	v   string
}

func (o *optString) String() string { return o.v }

func (o *optString) Set(v string) error {
	o.v = v
	o.set = true
	return nil
}
