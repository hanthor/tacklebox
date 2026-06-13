package main

import (
	"reflect"
	"testing"
)

func TestDedupSorted(t *testing.T) {
	got := dedupSorted([]string{"bazzite", "bluefin", "bazzite", "aurora"})
	want := []string{"aurora", "bazzite", "bluefin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilterEnvs(t *testing.T) {
	envs := envList("bluefin", "bazzite", "aurora")
	got := filterEnvs(envs, map[string]bool{"bazzite": true})
	if want := []string{"bluefin", "aurora"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
}

func TestFilterEnvsDropAll(t *testing.T) {
	envs := envList("bluefin")
	got := filterEnvs(envs, map[string]bool{"bluefin": true})
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", ids(got))
	}
}
