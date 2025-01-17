/*
* LSST Data Management System
* See COPYRIGHT file at the top of the source tree.
*
* This product includes software developed by the
* LSST Project (http://www.lsst.org/).
*
* This program is free software: you can redistribute it and/or modify
* it under the terms of the GNU General Public License as published by
* the Free Software Foundation, either version 3 of the License, or
* (at your option) any later version.
*
* This program is distributed in the hope that it will be useful,
* but WITHOUT ANY WARRANTY; without even the implied warranty of
* MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
* GNU General Public License for more details.
*
* You should have received a copy of the LSST License Statement and
* the GNU General Public License along with this program. If not,
* see <http://www.lsstcorp.org/LegalNotices/>.
 */

// Generate dbbench.ini file from Qserv integration tests's datasets
// See qserv/itest_src/datasets/case<ID>/queries
// Exemple to run it:
// go run itest/examples/dbbench.go && cat /tmp/dbbench.ini

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"gopkg.in/yaml.v3"
)

func check(e error) {
	if e != nil {
		panic(e)
	}
}

type SkippedQueries struct {
	CaseId   string
	QueryIds []string
}

func (t *SkippedQueries) descend(node *yaml.Node) error {
	found := false
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			t.descend(item)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Value != "id" {
				// log.Printf("%v", key.Value)
				t.descend(value)
				continue
			}
			if value.Kind != yaml.ScalarNode {
				return errors.New("encountered non-scalar task")
			}
			if value.Value == t.CaseId {
				// log.Printf("FOUND")
				found = true
				break
			}
		}
		if found {
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				value := node.Content[i+1]
				if key.Kind != yaml.ScalarNode || key.Value != "skip_numbers" {
					// log.Printf("%v", key.Value)
					continue
				}
				if value.Kind != yaml.SequenceNode {
					return errors.New("encountered non-list task")
				}
				for _, item := range value.Content {
					// log.Printf("XXX FOUND %v", item.Value)
					t.QueryIds = append(t.QueryIds, item.Value)
				}

			}
		}
	}
	return nil
}

func (t *SkippedQueries) UnmarshalYAML(value *yaml.Node) error {
	t.QueryIds = nil
	return t.descend(value)
}

func getSkippedQueries(filename string, caseId string) ([]string, error) {

	var t SkippedQueries
	t.CaseId = caseId

	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(buf, &t)
	if err != nil {
		return nil, fmt.Errorf("in file %q: %v", filename, err)
	}

	for _, item := range t.QueryIds {
		fmt.Printf("%v", item)
	}

	return t.QueryIds, nil
}

func generateCountQueries(filename string) (string, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("in file %q: %v", filename, err)
	}

	json_str := string(b) // convert content to a 'string'

	obj, err := oj.ParseString(json_str)
	if err != nil {
		return "", fmt.Errorf("in json %q, %v", json_str, err)
	}

	jsonpath := ".tables.*.schema"
	x, err := jp.ParseString(jsonpath)
	if err != nil {
		return "", fmt.Errorf("in jsonpath %q, %v", jsonpath, err)
	}
	ys := x.Get(obj)
	out := ""
	for _, v := range ys {
		table := strings.TrimSuffix(fmt.Sprint(v), ".json")
		query := fmt.Sprintf("[count%s]\nquery=SELECT count(*) FROM %s\n"+
			"query-results-file=/tmp/dbbench/count%s.csv\ncount=1\n\n", table, table, table)
		out = out + query
	}
	return out, nil
}

func main() {

	var sqlFiles []fs.FileInfo

	qserv_src_path := flag.String("qserv_src_path", "/home/fjammes/src/qserv/", "Path to Qserv source code")
	case_id := flag.String("case_id", "case01", "Test case to extract")
	dbbench_conf := flag.String("dbbench_conf", "/tmp/dbbench.ini", "Path to dbbench output file")
	flag.Parse()

	query_path := filepath.Join(*qserv_src_path, "itest_src", "datasets", *case_id, "queries")
	metadata_path := filepath.Join(*qserv_src_path, "itest_src", "datasets", *case_id, "data", "ingest", "metadata.json")

	log.Printf("Use input queries  path %v", query_path)

	conf_file := filepath.Join(*qserv_src_path, "src", "admin", "etc", "integration_tests.yaml")
	skippedQueryIds, err := getSkippedQueries(conf_file, *case_id)
	check(err)

	files, err := ioutil.ReadDir(query_path)
	check(err)

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".sql") {
			skipped := false
			for _, skippedId := range skippedQueryIds {
				if strings.HasPrefix(file.Name(), skippedId) {
					skipped = true
				}
			}
			if !skipped {
				sqlFiles = append(sqlFiles, file)
			}
		}
	}

	log.Printf("Generate %s", *dbbench_conf)
	f, err := os.Create(*dbbench_conf)
	check(err)

	defer f.Close()
	w := bufio.NewWriter(f)
	i := 0
	countQueries, err := generateCountQueries(metadata_path)
	check(err)
	w.WriteString(countQueries)
	for _, file := range sqlFiles {

		id := fmt.Sprintf("[%d]\n", i)
		_, err := w.WriteString(id)
		check(err)
		comment := fmt.Sprintf("; %s\n", file.Name())
		_, err = w.WriteString(comment)
		check(err)
		filename := filepath.Join(query_path, file.Name())

		sql := getSQL(filename)
		query := fmt.Sprintf("query=%s\n", sql)
		_, err = w.WriteString(query)
		check(err)

		query_results_file := fmt.Sprintf("query-results-file=/tmp/dbbench/%d.csv\n", i)
		_, err = w.WriteString(query_results_file)

		_, err = w.WriteString("count=1\n\n")

		i++
		w.Flush()
	}
}

func getSQL(filename string) string {
	sql := ""
	f, err := os.OpenFile(filename, os.O_RDONLY, os.ModePerm)
	check(err)
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text() // GET the line string
		data := strings.TrimSuffix(line, "\n")
		// Remove ending comment
		data = strings.Split(data, "--")[0]
		data = strings.Split(data, ";")[0]
		data = strings.TrimSpace(data)
		if len(data) != 0 {
			data += " "
			space := regexp.MustCompile(`\s+`)
			//log.Printf("data1 '%v' '%s'\n  ", data, sql)
			data = space.ReplaceAllString(data, " ")
			sql += data
		}
	}
	//log.Printf("SQL '%v'\n  ", sql)
	sql = strings.TrimSuffix(sql, " ")

	check(sc.Err())
	return sql
}
