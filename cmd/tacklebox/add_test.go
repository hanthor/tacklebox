package main

import (
	"reflect"
	"testing"

	"github.com/tuna-os/tacklebox/internal/recipe"
)

func envList(ids ...string) []recipe.BootableEnvironment {
	out := make([]recipe.BootableEnvironment, len(ids))
	for i, id := range ids {
		out[i] = recipe.BootableEnvironment{ID: id, Image: id + ":latest"}
	}
	return out
}

func ids(envs []recipe.BootableEnvironment) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.ID
	}
	return out
}

func TestSelectEnvsToAdd_NoFilterSkipsPresent(t *testing.T) {
	envs := envList("bluefin", "bazzite", "aurora")
	present := map[string]bool{"bluefin": true}

	got, err := selectEnvsToAdd(envs, present, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bazzite", "aurora"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
}

func TestSelectEnvsToAdd_FilterSelectsExact(t *testing.T) {
	envs := envList("bluefin", "bazzite", "aurora")
	present := map[string]bool{}

	got, err := selectEnvsToAdd(envs, present, []string{"aurora"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"aurora"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v", ids(got), want)
	}
}

func TestSelectEnvsToAdd_UnknownFilterErrors(t *testing.T) {
	envs := envList("bluefin")
	_, err := selectEnvsToAdd(envs, map[string]bool{}, []string{"ghost"})
	if err == nil {
		t.Fatal("expected error for unknown env in filter")
	}
}

func TestSelectEnvsToAdd_AlreadyPresentFilterErrors(t *testing.T) {
	envs := envList("bluefin")
	_, err := selectEnvsToAdd(envs, map[string]bool{"bluefin": true}, []string{"bluefin"})
	if err == nil {
		t.Fatal("expected error when filtering an already-present env")
	}
}
