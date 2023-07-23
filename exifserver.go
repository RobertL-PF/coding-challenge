package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"sync"
)

type Desc struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",innerxml"`
}

type Tag struct {
	ID           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	Type         string `xml:"type,attr"`
	Writable     bool   `xml:"writable,attr"`
	Descriptions []Desc `xml:"desc"`
}

type Table struct {
	XMLName xml.Name `xml:"table"`
	Name    string   `xml:"name,attr"`
	Tags    []Tag    `xml:"tag"`
}

type Output struct {
	Writable bool              `json:"writable"`
	Path     string            `json:"path"`
	Group    string            `json:"group"`
	Desc     map[string]string `json:"description"`
	Type     string            `json:"type"`
}

func nextTable(dec *xml.Decoder) (*Table, error) {
	for {
		t := &Table{}
		err := dec.Decode(t)
		if err != nil {
			if _, ok := err.(xml.UnmarshalError); !ok {
				return nil, err
			}
			continue
		}

		return t, nil
	}
}

func writeJson(w http.ResponseWriter, tc <-chan Table, wg *sync.WaitGroup) {
	w.Header().Set("Content-Type", "application/json")

	_, err := w.Write([]byte(`{"tags": [`))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	enc := json.NewEncoder(w)
	needsComma := false
	for table := range tc {
		for _, tag := range table.Tags {
			if needsComma {
				_, err = w.Write([]byte(","))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}

			descriptions := make(map[string]string)
			for _, desc := range tag.Descriptions {
				descriptions[desc.Lang] = desc.Value
			}

			err = enc.Encode(Output{
				Writable: tag.Writable,
				Path:     fmt.Sprintf("%s:%s", table.Name, tag.Name),
				Group:    tag.Name,
				Desc:     descriptions,
				Type:     tag.Type,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			needsComma = true
		}

		wg.Done()
	}

	_, err = w.Write([]byte(`]}`))
	if err != nil {
		log.Fatal(err)
	}

	wg.Done()
}

func tableIterator(dec *xml.Decoder, c chan<- Table, wg *sync.WaitGroup) error {
	for {
		table, err := nextTable(dec)
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				break
			}
			return err
		}

		if table == nil {
			break
		}

		wg.Add(1)
		c <- *table
	}

	return nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tags" {
		http.Error(w, "Das sind nicht die Droiden, die ihr sucht", http.StatusNotFound)
		return
	}

	rdr, wrt := io.Pipe()

	cmd := exec.CommandContext(r.Context(), "exiftool", "-listx")
	cmd.Stdout = wrt
	err := cmd.Start()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Fatal(err)
		}

		rdr.Close()
		wrt.Close()
	}()

	wg := &sync.WaitGroup{}
	c := make(chan Table, 10)
	go writeJson(w, c, wg)

	dec := xml.NewDecoder(rdr)
	err = tableIterator(dec, c, wg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	wg.Wait() // wait for table handling completion

	wg.Add(1)
	close(c)
	wg.Wait() // wait for json closing tags
}

func main() {
	log.SetFlags(log.Lshortfile)

	log.Fatal(http.ListenAndServe("127.0.0.1:8080", http.HandlerFunc(handler)))
}
