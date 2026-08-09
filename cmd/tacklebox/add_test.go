package main

import (
	"reflect"
	"strings"
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

func TestSelectEnvsToAdd_AllAlreadyPresentReturnsEmpty(t *testing.T) {
	envs := envList("bluefin", "bazzite")
	present := map[string]bool{"bluefin": true, "bazzite": true}
	got, err := selectEnvsToAdd(envs, present, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty when all envs are already present, got %v", ids(got))
	}
}

func TestSelectEnvsToAdd_MixedUnknownAndAlreadyPresent(t *testing.T) {
	envs := envList("a", "b", "c")
	present := map[string]bool{"b": true}
	// "a" is unknown to recipe, "b" is already present, "c" should succeed.
	// selectEnvsToAdd returns the first error (unknown takes priority over already-present).
	_, err := selectEnvsToAdd(envs, present, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("expected error for mixed unknown+already-present filter")
	}
	if !strings.Contains(err.Error(), "recipe has no env") {
		t.Errorf("expected 'recipe has no env' error, got: %v", err)
	}
}

func TestSelectEnvsToAdd_FilterPreservesOrder(t *testing.T) {
	envs := envList("c", "a", "b")
	present := map[string]bool{}
	got, err := selectEnvsToAdd(envs, present, []string{"a", "c"})
	if err != nil {
		t.Fatal(err)
	}
	// Order follows the filter order, not recipe order.
	if want := []string{"a", "c"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v, want %v (filter order)", ids(got), want)
	}
}
