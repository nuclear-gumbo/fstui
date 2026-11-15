// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Pool is one pool from "zpool list -j --json-int" or "zpool get -j".
type Pool struct {
	Name       name.Pool
	State      string
	GUID       uint64
	Properties Properties
}

// DatasetType is the kind of a dataset.
type DatasetType string

// Dataset types as OpenZFS prints them.
const (
	Filesystem DatasetType = "FILESYSTEM"
	Volume     DatasetType = "VOLUME"
	Snapshot   DatasetType = "SNAPSHOT"
	Bookmark   DatasetType = "BOOKMARK"
)

// Dataset is one entry from "zfs list -j --json-int" or "zfs get -j".
// Name is validated according to Type.
type Dataset struct {
	Name       string
	Type       DatasetType
	Pool       name.Pool
	CreateTXG  uint64
	Properties Properties
}

type jsonPool struct {
	Name       string                  `json:"name"`
	State      string                  `json:"state"`
	GUID       jsonUint                `json:"pool_guid"`
	Properties map[string]jsonProperty `json:"properties"`
}

type jsonDataset struct {
	Name       string                  `json:"name"`
	Type       DatasetType             `json:"type"`
	Pool       string                  `json:"pool"`
	CreateTXG  jsonUint                `json:"createtxg"`
	Properties map[string]jsonProperty `json:"properties"`
}

// ParsePoolList parses "zpool list -j --json-int"; ParsePoolGet parses
// "zpool get -j --json-int". Both are sorted by name.
func ParsePoolList(data []byte) ([]Pool, error) { return parsePools(data, "zpool list") }

// ParsePoolGet parses "zpool get -j --json-int".
func ParsePoolGet(data []byte) ([]Pool, error) { return parsePools(data, "zpool get") }

func parsePools(data []byte, command string) ([]Pool, error) {
	// With no pools imported, zpool list -j prints nothing at all and
	// exits 0 (tested on 2.3.9), where zpool status -j prints an empty
	// pools object.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc struct {
		Pools map[string]jsonPool `json:"pools"`
	}
	if err := decode(data, command, &doc); err != nil {
		return nil, err
	}
	out := make([]Pool, 0, len(doc.Pools))
	for _, jp := range doc.Pools {
		pool, err := name.ParsePool(jp.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: pool %s: %w", command, termtext.New(jp.Name), err)
		}
		props, err := parseProperties(jp.Properties)
		if err != nil {
			return nil, fmt.Errorf("%s: pool %s: %w", command, pool, err)
		}
		out = append(out, Pool{Name: pool, State: jp.State, GUID: uint64(jp.GUID), Properties: props})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name.String() < out[j].Name.String() })
	return out, nil
}

// ParseDatasetList parses "zfs list -j --json-int"; ParseDatasetGet parses
// "zfs get -j --json-int". Both are sorted by name, which puts children
// directly after their parent.
func ParseDatasetList(data []byte) ([]Dataset, error) { return parseDatasets(data, "zfs list") }

// ParseDatasetGet parses "zfs get -j --json-int".
func ParseDatasetGet(data []byte) ([]Dataset, error) { return parseDatasets(data, "zfs get") }

func parseDatasets(data []byte, command string) ([]Dataset, error) {
	var doc struct {
		Datasets map[string]jsonDataset `json:"datasets"`
	}
	if err := decode(data, command, &doc); err != nil {
		return nil, err
	}
	out := make([]Dataset, 0, len(doc.Datasets))
	for _, jd := range doc.Datasets {
		d, err := jd.parse()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", command, err)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (jd jsonDataset) parse() (Dataset, error) {
	if err := validateDatasetName(jd.Name, jd.Type); err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: %w", termtext.New(jd.Name), err)
	}
	pool, err := name.ParsePool(jd.Pool)
	if err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: pool: %w", jd.Name, err)
	}
	props, err := parseProperties(jd.Properties)
	if err != nil {
		return Dataset{}, fmt.Errorf("dataset %s: %w", jd.Name, err)
	}
	return Dataset{Name: jd.Name, Type: jd.Type, Pool: pool, CreateTXG: uint64(jd.CreateTXG), Properties: props}, nil
}

func validateDatasetName(s string, t DatasetType) error {
	var err error
	switch t {
	case Filesystem, Volume:
		_, err = name.ParseDataset(s)
	case Snapshot:
		_, err = name.ParseSnapshot(s)
	case Bookmark:
		_, err = name.ParseBookmark(s)
	default:
		err = fmt.Errorf("unknown dataset type %q", t)
	}
	if err != nil {
		return fmt.Errorf("invalid name: %w", err)
	}
	return nil
}
