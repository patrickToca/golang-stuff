// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package downloader

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"launchpad.net/tomb"

	"launchpad.net/juju-core/log"
)

// Status represents the status of a completed download.
type Status struct {
	// File holds the downloaded data on success.
	File *os.File
	// Err describes any error encountered while downloading.
	Err error
}

// Download can download a file from the network.
type Download struct {
	tomb tomb.Tomb
	done chan Status
}

// New returns a new Download instance downloading from the given URL to
// the given directory. If dir is empty, it defaults to os.TempDir().
func New(url, dir string) *Download {
	d := &Download{
		done: make(chan Status),
	}
	go d.run(url, dir)
	return d
}

// Stop stops any download that's in progress.
func (d *Download) Stop() {
	d.tomb.Kill(nil)
	d.tomb.Wait()
}

// Done returns a channel that receives a status when the download has
// completed.  It is the receiver's responsibility to close and remove
// the received file.
func (d *Download) Done() <-chan Status {
	return d.done
}

func (d *Download) run(url, dir string) {
	defer d.tomb.Done()
	file, err := download(url, dir)
	if err != nil {
		err = fmt.Errorf("cannot download %q: %v", url, err)
	}
	status := Status{
		File: file,
		Err:  err,
	}
	select {
	case d.done <- status:
	case <-d.tomb.Dying():
		cleanTempFile(status.File)
	}
}

func download(url, dir string) (file *os.File, err error) {
	if dir == "" {
		dir = os.TempDir()
	}
	tempFile, err := ioutil.TempFile(dir, "inprogress-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanTempFile(tempFile)
		}
	}()
	// TODO(rog) make the download operation interruptible.
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad http response: %v", resp.Status)
	}
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return nil, err
	}
	if _, err := tempFile.Seek(0, 0); err != nil {
		return nil, err
	}
	return tempFile, nil
}

func cleanTempFile(f *os.File) {
	if f != nil {
		f.Close()
		if err := os.Remove(f.Name()); err != nil {
			log.Warningf("downloader: cannot remove temp file %q: %v", f.Name(), err)
		}
	}
}
