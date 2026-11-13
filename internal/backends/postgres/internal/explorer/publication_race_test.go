package postgres

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

const pubAuthRaceRounds = 12

type pubAuthAttempt struct {
	ctx      context.Context
	ps       *PublishStore
	receipt  explorer.CompilationReceipt
	revision explorer.Revision
	release  dataset.ProjectRelease
	expected int64
	out      *explorer.Revision
	err      error
}

func runPubAuthRace(t *testing.T, a, b *pubAuthAttempt) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, at := range []*pubAuthAttempt{a, b} {
		wg.Add(1)
		go func(at *pubAuthAttempt) {
			defer wg.Done()
			<-start
			at.out, at.err = at.ps.PublishAuthoring(at.ctx, at.receipt, at.revision, at.release, at.expected)
		}(at)
	}
	close(start)
	wg.Wait()
}

func TestPublishAuthoringRacingIdenticalCandidateConverges(t *testing.T) {
	for round := range pubAuthRaceRounds {
		t.Run(roundName(round), func(t *testing.T) {
			project, explorerID := "proj_pubauth_race_same", "exp_race_same"
			tag0, tagRace := "r0", "rx"

			fr := newPGPubAuthFixture(t)
			fr.migrate(t)
			storeR := fr.newStore(t)
			rcpt0, rev0, rel0 := fr.seedPublishedWorld(t, storeR, project, explorerID, tag0)
			fr.publishOK(t, fr.newPublishStore(t), rcpt0, rev0, rel0, 0)

			raceReceipt, raceRevision, raceRelease :=
				secondPublishInputsSameExplorer(t, fr, project, explorerID, tagRace)
			psA, pidA := fr.newDedicatedPublishStore(t)
			psB, pidB := fr.newDedicatedPublishStore(t)
			requireDistinctBackendsPubAuth(t, pidA, pidB, "publisher A", "publisher B")

			a := &pubAuthAttempt{ctx: fr.ctx, ps: psA, receipt: raceReceipt, revision: raceRevision, release: raceRelease, expected: 1}
			b := &pubAuthAttempt{ctx: fr.ctx, ps: psB, receipt: raceReceipt, revision: raceRevision, release: raceRelease, expected: 1}
			runPubAuthRace(t, a, b)
			if a.err != nil {
				t.Fatalf("publisher A: %v", a.err)
			}
			if b.err != nil {
				t.Fatalf("publisher B: %v", b.err)
			}
			if got := fr.countRows(t, fr.lanesTable("active_project_release"), "project = $1 AND revision = 2", project); got != 1 {
				t.Fatalf("pointer is not exactly at revision 2 after the race (%d)", got)
			}
			if got := fr.countRows(t, fr.ctlTable(RevisionsTable), "project = $1", project); got != 2 {
				t.Fatalf("%d revision rows after identical race, want 2", got)
			}

			fg := newPGPubAuthFixture(t)
			fg.migrate(t)
			storeG := fg.newStore(t)
			gReceipt0, gRevision0, gRelease0 := fg.seedPublishedWorld(t, storeG, project, explorerID, tag0)
			fg.publishOK(t, fg.newPublishStore(t), gReceipt0, gRevision0, gRelease0, 0)
			gReceipt, gRevision, gRelease :=
				secondPublishInputsSameExplorer(t, fg, project, explorerID, tagRace)
			fg.publishOK(t, fg.newPublishStore(t), gReceipt, gRevision, gRelease, 1)
			if raced, solo := fr.snapshotForProject(t, project), fg.snapshotForProject(t, project); raced != solo {
				t.Fatalf("identical-candidate race diverged from the solo golden:\nraced:\n%s\nsolo:\n%s", raced, solo)
			}
		})
	}
}

func TestPublishAuthoringRacingDifferentCandidatesSingleWinner(t *testing.T) {
	for round := range pubAuthRaceRounds {
		t.Run(roundName(round), func(t *testing.T) {
			project, explorerID := "proj_pubauth_race_diff", "exp_race_diff"
			fr := newPGPubAuthFixture(t)
			fr.migrate(t)
			storeR := fr.newStore(t)
			rcpt0, rev0, rel0 := fr.seedPublishedWorld(t, storeR, project, explorerID, "r0")
			fr.publishOK(t, fr.newPublishStore(t), rcpt0, rev0, rel0, 0)

			receiptX, revisionX, releaseX :=
				secondPublishInputsSameExplorer(t, fr, project, explorerID, "candX")
			receiptY, revisionY, releaseY :=
				secondPublishInputsSameExplorer(t, fr, project, explorerID, "candY")
			psA, pidA := fr.newDedicatedPublishStore(t)
			psB, pidB := fr.newDedicatedPublishStore(t)
			requireDistinctBackendsPubAuth(t, pidA, pidB, "publisher X", "publisher Y")

			a := &pubAuthAttempt{ctx: fr.ctx, ps: psA, receipt: receiptX, revision: revisionX, release: releaseX, expected: 1}
			b := &pubAuthAttempt{ctx: fr.ctx, ps: psB, receipt: receiptY, revision: revisionY, release: releaseY, expected: 1}
			runPubAuthRace(t, a, b)

			loser, winnerName, winnerReleaseID, winnerRevisionID := singleRaceWinner(t, a, b)
			if !errors.Is(loser.err, dataset.ErrReleaseActivationConflict) {
				t.Fatalf("loser error = %v, want dataset.ErrReleaseActivationConflict", loser.err)
			}
			if got := fr.countRows(t, fr.ctlTable(RevisionsTable), "id = $1", loser.revision.ID); got != 0 {
				t.Fatalf("loser revision row %q survived the lost race", loser.revision.ID)
			}
			if got := fr.countRows(t, fr.ctlTable(ReceiptsTable), "id = $1 AND project = $2", loser.receipt.ID, project); got != 0 {
				t.Fatalf("loser receipt row %q survived the lost race", loser.receipt.ID)
			}
			var pointerRelease string
			var pointerRevision int64
			if err := fr.iso.QueryRow(fr.ctx, "SELECT release_id, revision FROM "+
				fr.lanesTable("active_project_release")+" WHERE project = $1", project).
				Scan(&pointerRelease, &pointerRevision); err != nil {
				fr.fail("read raced pointer", err)
			}
			if pointerRelease != winnerReleaseID || pointerRevision != 2 {
				t.Fatalf("raced pointer = %s@%d, want %s@2", pointerRelease, pointerRevision, winnerReleaseID)
			}
			var ownerActive string
			if err := fr.iso.QueryRow(fr.ctx, "SELECT active_revision_id FROM "+
				fr.ctlTable(OwnerTable)+" WHERE project = $1", project).Scan(&ownerActive); err != nil {
				fr.fail("read raced owner", err)
			}
			if ownerActive != winnerRevisionID {
				t.Fatalf("raced owner = %s, want %s (%s won)", ownerActive, winnerRevisionID, winnerName)
			}

			var trueExpected int64 = 2
			loser.expected = trueExpected
			if _, err := loser.ps.PublishAuthoring(fr.ctx, loser.receipt, loser.revision, loser.release, loser.expected); err != nil {
				t.Fatalf("loser retry: %v", err)
			}
			fg := newPGPubAuthFixture(t)
			fg.migrate(t)
			storeG := fg.newStore(t)
			gReceipt0, gRevision0, gRelease0 := fg.seedPublishedWorld(t, storeG, project, explorerID, "r0")
			fg.publishOK(t, fg.newPublishStore(t), gReceipt0, gRevision0, gRelease0, 0)
			firstReceipt, firstRevision, firstRelease :=
				secondPublishInputsSameExplorer(t, fg, project, explorerID, "candX")
			secondReceipt, secondRevision, secondRelease :=
				secondPublishInputsSameExplorer(t, fg, project, explorerID, "candY")
			if winnerName == "X" {
				fg.publishOK(t, fg.newPublishStore(t), firstReceipt, firstRevision, firstRelease, 1)
				fg.publishOK(t, fg.newPublishStore(t), secondReceipt, secondRevision, secondRelease, 2)
			} else {
				fg.publishOK(t, fg.newPublishStore(t), secondReceipt, secondRevision, secondRelease, 1)
				fg.publishOK(t, fg.newPublishStore(t), firstReceipt, firstRevision, firstRelease, 2)
			}
			if raced, solo := fr.snapshotForProject(t, project), fg.snapshotForProject(t, project); raced != solo {
				t.Fatalf("different-candidate race diverged from the solo golden:\nraced:\n%s\nsolo:\n%s", raced, solo)
			}
		})
	}
}

func singleRaceWinner(t *testing.T, a, b *pubAuthAttempt) (loser *pubAuthAttempt, winnerName, winnerReleaseID, winnerRevisionID string) {
	t.Helper()
	aOK, bOK := a.err == nil, b.err == nil
	if aOK && bOK {
		t.Fatal("both racing publishers committed: the pointer CAS admitted two winners")
	}
	if !aOK && !bOK {
		t.Fatalf("both racing publishers failed: A=%v B=%v, want exactly one winner", a.err, b.err)
	}
	if aOK {
		return b, "X", a.release.ID, a.revision.ID
	}
	return a, "Y", b.release.ID, b.revision.ID
}

func roundName(round int) string { return "round-" + strconv.Itoa(round) }
