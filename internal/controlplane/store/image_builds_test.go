package store

import (
	"context"
	"errors"
	"testing"
)

func TestImageBuildLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)
	source := "services:\n  api:\n    build: .\n"
	fileID, err := st.CreateComposeFile(ctx, "build-test-"+serverID[:8], source, "")
	if err != nil {
		t.Fatal(err)
	}
	depID, err := st.InsertDeployment(ctx, NewDeployment{ComposeFileID: &fileID, ServerID: serverID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(context.Background(), `DELETE FROM deployments WHERE id = $1`, depID)
		_, _ = st.pool.Exec(context.Background(), `DELETE FROM compose_files WHERE id = $1`, fileID)
	})

	// A revision whose images were built deploys its pinned content, and
	// that's what redeploy paths outside the rollout (backup restore) get.
	tag := "pspe-build/x-api:" + serverID[:8]
	pinned := "services:\n  api:\n    image: " + tag + "\n"
	rev, err := st.RecordRevision(ctx, NewRevision{DeploymentID: depID, Action: "deploy", ComposeContent: pinned, SourceContent: source, ChangeSummary: "code a → b"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.GetDeployment(ctx, depID)
	if err != nil {
		t.Fatal(err)
	}
	if _, content, err := st.ResolveDeploymentSource(ctx, dep); err != nil || content != pinned {
		t.Fatalf("ResolveDeploymentSource = %q, %v", content, err)
	}
	r, err := st.GetDeploymentRevision(ctx, depID, rev)
	if err != nil || r.SourceOf() != source || r.ChangeSummary != "code a → b" {
		t.Fatalf("revision = %+v, %v", r, err)
	}

	id, err := st.InsertImageBuild(ctx, NewImageBuild{
		DeploymentID: depID, Revision: rev, ServerID: serverID, Service: "api", ImageTag: tag,
		GitCommit: "abc", ContextPath: ".", Dockerfile: "Dockerfile", BuildArgs: map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Output batches apply once, in order; a replayed batch is ignored.
	for _, u := range []BuildUpdate{
		{Status: BuildCloning, Message: "cloning"},
		{Status: BuildBuilding, Log: "one\n", LogSeq: 1},
		{Status: BuildBuilding, Log: "one\n", LogSeq: 1},
		{Status: BuildBuilding, Log: "two\n", LogSeq: 2},
	} {
		if err := st.UpdateImageBuild(ctx, id, u); err != nil {
			t.Fatal(err)
		}
	}
	b, err := st.GetImageBuild(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != BuildBuilding || b.Log != "one\ntwo\n" || b.LogSeq != 2 || b.StartedAt == nil {
		t.Fatalf("build = %+v", b)
	}
	if len(b.BuildArgNames) != 2 || b.BuildArgNames[0] != "A" {
		t.Fatalf("arg names = %v", b.BuildArgNames)
	}

	// A newer revision supersedes it; the agent's late "cancelled" doesn't
	// overwrite that, but its last output is still kept.
	active, err := st.SupersedeActiveBuilds(ctx, depID, rev+1, "superseded by revision 2")
	if err != nil || len(active) != 1 || active[0].ID != id || active[0].ServerID != serverID {
		t.Fatalf("SupersedeActiveBuilds = %v, %v", active, err)
	}
	if err := st.UpdateImageBuild(ctx, id, BuildUpdate{Status: BuildCancelled, Message: "build cancelled", Log: "three\n", LogSeq: 3}); err != nil {
		t.Fatal(err)
	}
	state, err := st.GetImageBuildState(ctx, id)
	if err != nil || state.Status != BuildSuperseded || state.Message != "superseded by revision 2" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	if changed, err := st.FinishImageBuild(ctx, id, BuildFailed, "late"); err != nil || changed {
		t.Fatalf("FinishImageBuild on a final build: %v, %v", changed, err)
	}
	if _, err := st.LatestBuildForTag(ctx, tag); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a superseded build must not count as built: %v", err)
	}

	// A successful build is how the image can be rebuilt later.
	id2, err := st.InsertImageBuild(ctx, NewImageBuild{DeploymentID: depID, Revision: rev + 1, ServerID: serverID, Service: "api", ImageTag: tag, GitCommit: "abc", ContextPath: ".", Dockerfile: "Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateImageBuild(ctx, id2, BuildUpdate{Status: BuildSucceeded, ImageID: "sha256:1", Reused: true}); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestBuildForTag(ctx, tag)
	if err != nil || latest.ID != id2 || !latest.Reused || latest.ImageID != "sha256:1" || latest.FinishedAt == nil {
		t.Fatalf("LatestBuildForTag = %+v, %v", latest, err)
	}
	list, err := st.ListDeploymentBuilds(ctx, depID, 10)
	if err != nil || len(list) != 2 || list[0].ID != id2 || list[0].Log != "" {
		t.Fatalf("ListDeploymentBuilds = %+v, %v", list, err)
	}
}
