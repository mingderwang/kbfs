// Copyright 2016 Keybase Inc. All rights reserved.
// Use of this source code is governed by a BSD
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keybase/client/go/libkb"
	"github.com/keybase/client/go/logger"
	"github.com/keybase/client/go/protocol/keybase1"
	"github.com/keybase/kbfs/libkbfs"
	"golang.org/x/net/context"
)

// LibKBFS implements the Engine interface for direct test harness usage of libkbfs.
type LibKBFS struct {
	// hack: hold references on behalf of the test harness
	refs map[libkbfs.Config]map[libkbfs.Node]bool
	// channels used to re-enable updates if disabled
	updateChannels map[libkbfs.Config]map[libkbfs.FolderBranch]chan<- struct{}
	// test object, mostly for logging
	t testing.TB
	// timeout for all KBFS calls
	opTimeout time.Duration
	// journal directory
	journalDir string
}

// Check that LibKBFS fully implements the Engine interface.
var _ Engine = (*LibKBFS)(nil)

// Name returns the name of the Engine.
func (k *LibKBFS) Name() string {
	return "libkbfs"
}

// Init implements the Engine interface.
func (k *LibKBFS) Init() {
	// Initialize reference holder and channels maps
	k.refs = make(map[libkbfs.Config]map[libkbfs.Node]bool)
	k.updateChannels =
		make(map[libkbfs.Config]map[libkbfs.FolderBranch]chan<- struct{})
}

// InitTest implements the Engine interface.
func (k *LibKBFS) InitTest(t testing.TB, blockSize int64, blockChangeSize int64,
	bwKBps int, opTimeout time.Duration, users []libkb.NormalizedUsername,
	clock libkbfs.Clock, journal bool) map[libkb.NormalizedUsername]User {
	// Start a new log for this test.
	k.t = t
	k.t.Log("\n------------------------------------------")
	userMap := make(map[libkb.NormalizedUsername]User)
	// create the first user specially
	config := libkbfs.MakeTestConfigOrBust(t, users...)

	setBlockSizes(t, config, blockSize, blockChangeSize)
	maybeSetBw(t, config, bwKBps)
	k.opTimeout = opTimeout

	config.SetClock(clock)
	userMap[users[0]] = config
	k.refs[config] = make(map[libkbfs.Node]bool)
	k.updateChannels[config] = make(map[libkbfs.FolderBranch]chan<- struct{})

	// create the rest of the users as copies of the original config
	for _, name := range users[1:] {
		c := libkbfs.ConfigAsUser(config, name)
		c.SetClock(clock)
		userMap[name] = c
		k.refs[c] = make(map[libkbfs.Node]bool)
		k.updateChannels[c] = make(map[libkbfs.FolderBranch]chan<- struct{})
	}

	if journal {
		jdir, err := ioutil.TempDir(os.TempDir(), "kbfs_journal")
		if err != nil {
			k.t.Fatalf("Couldn't enable journaling: %v", err)
		}
		k.journalDir = jdir
		k.t.Logf("Journal directory: %s", k.journalDir)
		for name, c := range userMap {
			c.(*libkbfs.ConfigLocal).EnableJournaling(
				filepath.Join(jdir, name.String()),
				libkbfs.TLFJournalBackgroundWorkEnabled)
		}
	}

	return userMap
}

const (
	// CtxOpID is the display name for the unique operation test ID tag.
	CtxOpID = "TID"
)

// CtxTagKey is the type used for unique context tags
type CtxTagKey int

const (
	// CtxIDKey is the type of the tag for unique operation IDs.
	CtxIDKey CtxTagKey = iota
)

func (k *LibKBFS) newContext() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	var cancel context.CancelFunc
	if k.opTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, k.opTimeout)
	} else {
		cancel = func() {}
	}

	id, errRandomRequestID := libkbfs.MakeRandomRequestID()
	ctx, err := libkbfs.NewContextWithCancellationDelayer(libkbfs.NewContextReplayable(
		ctx, func(ctx context.Context) context.Context {
			logTags := make(logger.CtxLogTags)
			logTags[CtxIDKey] = CtxOpID
			ctx = logger.NewContextWithLogTags(ctx, logTags)

			// Add a unique ID to this context, identifying a particular
			// request.
			if errRandomRequestID == nil {
				ctx = context.WithValue(ctx, CtxIDKey, id)
			}

			return ctx
		}))
	if err != nil {
		panic(err)
	}

	return ctx, func() {
		libkbfs.CleanupCancellationDelayer(ctx)
		cancel()
	}
}

// GetUID implements the Engine interface.
func (k *LibKBFS) GetUID(u User) (uid keybase1.UID) {
	config, ok := u.(libkbfs.Config)
	if !ok {
		panic("passed parameter isn't a config object")
	}
	var err error
	ctx, cancel := k.newContext()
	defer cancel()
	_, uid, err = config.KBPKI().GetCurrentUserInfo(ctx)
	if err != nil {
		panic(err.Error())
	}
	return uid
}

func parseTlfHandle(
	ctx context.Context, kbpki libkbfs.KBPKI, tlfName string, isPublic bool) (
	h *libkbfs.TlfHandle, err error) {
	// Limit to one non-canonical name for now.
outer:
	for i := 0; i < 2; i++ {
		h, err = libkbfs.ParseTlfHandle(ctx, kbpki, tlfName, isPublic)
		switch err := err.(type) {
		case nil:
			break outer
		case libkbfs.TlfNameNotCanonical:
			tlfName = err.NameToTry
		default:
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

// GetFavorites implements the Engine interface.
func (k *LibKBFS) GetFavorites(u User, public bool) (map[string]bool, error) {
	config := u.(*libkbfs.ConfigLocal)
	ctx, cancel := k.newContext()
	defer cancel()
	favorites, err := config.KBFSOps().GetFavorites(ctx)
	if err != nil {
		return nil, err
	}
	favoritesMap := make(map[string]bool)
	for _, f := range favorites {
		if f.Public != public {
			continue
		}
		favoritesMap[f.Name] = true
	}
	return favoritesMap, nil
}

// GetRootDir implements the Engine interface.
func (k *LibKBFS) GetRootDir(u User, tlfName string, isPublic bool, expectedCanonicalTlfName string) (
	dir Node, err error) {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	h, err := parseTlfHandle(ctx, config.KBPKI(), tlfName, isPublic)
	if err != nil {
		return nil, err
	}

	if string(h.GetCanonicalName()) != expectedCanonicalTlfName {
		return nil, fmt.Errorf("Expected canonical TLF name %s, got %s",
			expectedCanonicalTlfName, h.GetCanonicalName())
	}

	dir, _, err = config.KBFSOps().GetOrCreateRootNode(
		ctx, h, libkbfs.MasterBranch)
	if err != nil {
		return nil, err
	}
	k.refs[config][dir.(libkbfs.Node)] = true
	return dir, nil
}

// CreateDir implements the Engine interface.
func (k *LibKBFS) CreateDir(u User, parentDir Node, name string) (dir Node, err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	dir, _, err = kbfsOps.CreateDir(ctx, parentDir.(libkbfs.Node), name)
	if err != nil {
		return dir, err
	}
	k.refs[config][dir.(libkbfs.Node)] = true
	return dir, nil
}

// CreateFile implements the Engine interface.
func (k *LibKBFS) CreateFile(u User, parentDir Node, name string) (file Node, err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	file, _, err = kbfsOps.CreateFile(ctx, parentDir.(libkbfs.Node), name,
		false, libkbfs.NoExcl)
	if err != nil {
		return file, err
	}
	k.refs[config][file.(libkbfs.Node)] = true
	return file, nil
}

// CreateFileExcl implements the Engine interface.
func (k *LibKBFS) CreateFileExcl(u User, parentDir Node, name string) (file Node, err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	file, _, err = kbfsOps.CreateFile(ctx, parentDir.(libkbfs.Node), name, false, libkbfs.WithExcl)
	if err != nil {
		return nil, err
	}
	k.refs[config][file.(libkbfs.Node)] = true
	return file, nil
}

// CreateLink implements the Engine interface.
func (k *LibKBFS) CreateLink(u User, parentDir Node, fromName, toPath string) (err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	_, err = kbfsOps.CreateLink(ctx, parentDir.(libkbfs.Node), fromName, toPath)
	return err
}

// RemoveDir implements the Engine interface.
func (k *LibKBFS) RemoveDir(u User, dir Node, name string) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.RemoveDir(ctx, dir.(libkbfs.Node), name)
}

// RemoveEntry implements the Engine interface.
func (k *LibKBFS) RemoveEntry(u User, dir Node, name string) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.RemoveEntry(ctx, dir.(libkbfs.Node), name)
}

// Rename implements the Engine interface.
func (k *LibKBFS) Rename(u User, srcDir Node, srcName string,
	dstDir Node, dstName string) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.Rename(ctx, srcDir.(libkbfs.Node), srcName, dstDir.(libkbfs.Node), dstName)
}

// WriteFile implements the Engine interface.
func (k *LibKBFS) WriteFile(u User, file Node, data []byte, off int64, sync bool) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	err = kbfsOps.Write(ctx, file.(libkbfs.Node), data, off)
	if err != nil {
		return err
	}
	if sync {
		ctx, cancel := k.newContext()
		defer cancel()
		err = kbfsOps.Sync(ctx, file.(libkbfs.Node))
	}
	return err
}

// TruncateFile implements the Engine interface.
func (k *LibKBFS) TruncateFile(u User, file Node, size uint64, sync bool) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	err = kbfsOps.Truncate(ctx, file.(libkbfs.Node), size)
	if err != nil {
		return err
	}
	if sync {
		ctx, cancel := k.newContext()
		defer cancel()
		err = kbfsOps.Sync(ctx, file.(libkbfs.Node))
	}
	return err
}

// Sync implements the Engine interface.
func (k *LibKBFS) Sync(u User, file Node) (err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.Sync(ctx, file.(libkbfs.Node))
}

// ReadFile implements the Engine interface.
func (k *LibKBFS) ReadFile(u User, file Node, off int64, buf []byte) (length int, err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	var numRead int64
	ctx, cancel := k.newContext()
	defer cancel()
	numRead, err = kbfsOps.Read(ctx, file.(libkbfs.Node), buf, off)
	if err != nil {
		return 0, err
	}
	return int(numRead), nil
}

type libkbfsSymNode struct {
	parentDir Node
	name      string
}

// Lookup implements the Engine interface.
func (k *LibKBFS) Lookup(u User, parentDir Node, name string) (file Node, symPath string, err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	file, ei, err := kbfsOps.Lookup(ctx, parentDir.(libkbfs.Node), name)
	if err != nil {
		return file, symPath, err
	}
	if file != nil {
		k.refs[config][file.(libkbfs.Node)] = true
	}
	if ei.Type == libkbfs.Sym {
		symPath = ei.SymPath
	}
	if file == nil {
		// For symlnks, return a special kind of node that can be used
		// to look up stats about the symlink.
		return libkbfsSymNode{parentDir, name}, symPath, nil
	}
	return file, symPath, nil
}

// GetDirChildrenTypes implements the Engine interface.
func (k *LibKBFS) GetDirChildrenTypes(u User, parentDir Node) (childrenTypes map[string]string, err error) {
	kbfsOps := u.(*libkbfs.ConfigLocal).KBFSOps()
	var entries map[string]libkbfs.EntryInfo
	ctx, cancel := k.newContext()
	defer cancel()
	entries, err = kbfsOps.GetDirChildren(ctx, parentDir.(libkbfs.Node))
	if err != nil {
		return childrenTypes, err
	}
	childrenTypes = make(map[string]string)
	for name, entryInfo := range entries {
		childrenTypes[name] = entryInfo.Type.String()
	}
	return childrenTypes, nil
}

// SetEx implements the Engine interface.
func (k *LibKBFS) SetEx(u User, file Node, ex bool) (err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.SetEx(ctx, file.(libkbfs.Node), ex)
}

// SetMtime implements the Engine interface.
func (k *LibKBFS) SetMtime(u User, file Node, mtime time.Time) (err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	ctx, cancel := k.newContext()
	defer cancel()
	return kbfsOps.SetMtime(ctx, file.(libkbfs.Node), &mtime)
}

// GetMtime implements the Engine interface.
func (k *LibKBFS) GetMtime(u User, file Node) (mtime time.Time, err error) {
	config := u.(*libkbfs.ConfigLocal)
	kbfsOps := config.KBFSOps()
	var info libkbfs.EntryInfo
	ctx, cancel := k.newContext()
	defer cancel()
	if node, ok := file.(libkbfs.Node); ok {
		info, err = kbfsOps.Stat(ctx, node)
	} else if node, ok := file.(libkbfsSymNode); ok {
		// Stat doesn't work for symlinks, so use lookup
		_, info, err = kbfsOps.Lookup(ctx, node.parentDir.(libkbfs.Node),
			node.name)
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, info.Mtime), nil
}

// getRootNode is like GetRootDir, but doesn't check the canonical TLF
// name.
func getRootNode(ctx context.Context, config libkbfs.Config, tlfName string,
	isPublic bool) (libkbfs.Node, error) {
	h, err := parseTlfHandle(ctx, config.KBPKI(), tlfName, isPublic)
	if err != nil {
		return nil, err
	}

	// TODO: we should cache the root node, to more faithfully
	// simulate real-world callers and avoid unnecessary work.
	kbfsOps := config.KBFSOps()
	dir, _, err := kbfsOps.GetOrCreateRootNode(ctx, h, libkbfs.MasterBranch)
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// DisableUpdatesForTesting implements the Engine interface.
func (k *LibKBFS) DisableUpdatesForTesting(u User, tlfName string, isPublic bool) (err error) {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	if _, ok := k.updateChannels[config][dir.GetFolderBranch()]; ok {
		// Updates are already disabled.
		return nil
	}

	var c chan<- struct{}
	c, err = libkbfs.DisableUpdatesForTesting(config, dir.GetFolderBranch())
	if err != nil {
		return err
	}
	k.updateChannels[config][dir.GetFolderBranch()] = c
	// Also stop conflict resolution.
	err = libkbfs.DisableCRForTesting(config, dir.GetFolderBranch())
	if err != nil {
		return err
	}
	return nil
}

// MakeNaïveStaller implements the Engine interface.
func (*LibKBFS) MakeNaïveStaller(u User) *libkbfs.NaïveStaller {
	return libkbfs.NewNaïveStaller(u.(*libkbfs.ConfigLocal))
}

// ReenableUpdates implements the Engine interface.
func (k *LibKBFS) ReenableUpdates(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	c, ok := k.updateChannels[config][dir.GetFolderBranch()]
	if !ok {
		return fmt.Errorf("Couldn't re-enable updates for %s (public=%t)", tlfName, isPublic)
	}

	// Restart CR using a clean context, since we will cancel ctx when
	// we return.
	err = libkbfs.RestartCRForTesting(
		libkbfs.BackgroundContextWithCancellationDelayer(), config,
		dir.GetFolderBranch())
	if err != nil {
		return err
	}

	c <- struct{}{}
	close(c)
	delete(k.updateChannels[config], dir.GetFolderBranch())
	return nil
}

// SyncFromServerForTesting implements the Engine interface.
func (k *LibKBFS) SyncFromServerForTesting(u User, tlfName string, isPublic bool) (err error) {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	return config.KBFSOps().SyncFromServerForTesting(ctx, dir.GetFolderBranch())
}

// ForceQuotaReclamation implements the Engine interface.
func (k *LibKBFS) ForceQuotaReclamation(u User, tlfName string, isPublic bool) (err error) {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	return libkbfs.ForceQuotaReclamationForTesting(
		config, dir.GetFolderBranch())
}

// AddNewAssertion implements the Engine interface.
func (k *LibKBFS) AddNewAssertion(u User, oldAssertion, newAssertion string) error {
	config := u.(*libkbfs.ConfigLocal)
	return libkbfs.AddNewAssertionForTest(config, oldAssertion, newAssertion)
}

// Rekey implements the Engine interface.
func (k *LibKBFS) Rekey(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	return config.KBFSOps().Rekey(ctx, dir.GetFolderBranch().Tlf)
}

// EnableJournal implements the Engine interface.
func (k *LibKBFS) EnableJournal(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	jServer, err := libkbfs.GetJournalServer(config)
	if err != nil {
		return err
	}

	return jServer.Enable(ctx, dir.GetFolderBranch().Tlf,
		libkbfs.TLFJournalBackgroundWorkEnabled)
}

// PauseJournal implements the Engine interface.
func (k *LibKBFS) PauseJournal(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	jServer, err := libkbfs.GetJournalServer(config)
	if err != nil {
		return err
	}

	jServer.PauseBackgroundWork(ctx, dir.GetFolderBranch().Tlf)
	return nil
}

// ResumeJournal implements the Engine interface.
func (k *LibKBFS) ResumeJournal(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	jServer, err := libkbfs.GetJournalServer(config)
	if err != nil {
		return err
	}

	jServer.ResumeBackgroundWork(ctx, dir.GetFolderBranch().Tlf)
	return nil
}

// FlushJournal implements the Engine interface.
func (k *LibKBFS) FlushJournal(u User, tlfName string, isPublic bool) error {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return err
	}

	jServer, err := libkbfs.GetJournalServer(config)
	if err != nil {
		return err
	}

	return jServer.Flush(ctx, dir.GetFolderBranch().Tlf)
}

// UnflushedPaths implements the Engine interface.
func (k *LibKBFS) UnflushedPaths(u User, tlfName string, isPublic bool) (
	[]string, error) {
	config := u.(*libkbfs.ConfigLocal)

	ctx, cancel := k.newContext()
	defer cancel()
	dir, err := getRootNode(ctx, config, tlfName, isPublic)
	if err != nil {
		return nil, err
	}

	status, _, err := config.KBFSOps().FolderStatus(ctx, dir.GetFolderBranch())
	if err != nil {
		return nil, err
	}

	return status.Journal.UnflushedPaths, nil
}

// Shutdown implements the Engine interface.
func (k *LibKBFS) Shutdown(u User) error {
	config := u.(*libkbfs.ConfigLocal)
	// drop references
	k.refs[config] = make(map[libkbfs.Node]bool)
	delete(k.refs, config)
	// clear update channels
	k.updateChannels[config] = make(map[libkbfs.FolderBranch]chan<- struct{})
	delete(k.updateChannels, config)

	// Get the user name before shutting everything down.
	var userName libkb.NormalizedUsername
	if k.journalDir != "" {
		var err error
		userName, _, err =
			config.KBPKI().GetCurrentUserInfo(context.Background())
		if err != nil {
			return err
		}
	}

	// shutdown
	if err := config.Shutdown(); err != nil {
		return err
	}

	if k.journalDir != "" {
		// Remove the user journal.
		if err := os.RemoveAll(
			filepath.Join(k.journalDir, userName.String())); err != nil {
			return err
		}
		// Remove the overall journal dir if it's empty.
		if err := os.Remove(k.journalDir); err != nil {
			k.t.Logf("Journal dir %s not empty yet", k.journalDir)
		}
	}
	return nil
}
