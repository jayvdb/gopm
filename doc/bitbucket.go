// Copyright (c) 2013 GPMGo Members. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package doc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

var (
	BitbucketPattern = regexp.MustCompile(`^bitbucket\.org/(?P<owner>[a-z0-9A-Z_.\-]+)/(?P<repo>[a-z0-9A-Z_.\-]+)(?P<dir>/[a-z0-9A-Z_.\-/]*)?$`)
	bitbucketEtagRe  = regexp.MustCompile(`^(hg|git)-`)
)

// GetBitbucketDoc downloads tarball from bitbucket.org.
func GetBitbucketDoc(client *http.Client, match map[string]string, installGOPATH, commit string, cmdFlags map[string]bool) (*Package, []string, error) {
	// Check version control.
	if m := bitbucketEtagRe.FindStringSubmatch(commit); m != nil {
		match["vcs"] = m[1]
	} else {
		var repo struct {
			Scm string
		}
		if err := httpGetJSON(client, expand("https://api.bitbucket.org/1.0/repositories/{owner}/{repo}", match), &repo); err != nil {
			return nil, nil, err
		}
		match["vcs"] = repo.Scm
	}

	// bundle and snapshot will have commit 'B' and 'S',
	// but does not need to download dependencies.
	isCheckImport := len(commit) == 0

	// Check if download with specific revision.
	if isCheckImport || len(commit) == 1 {
		tags := make(map[string]string)
		for _, nodeType := range []string{"branches", "tags"} {
			var nodes map[string]struct {
				Node string
			}
			if err := httpGetJSON(client, expand("https://api.bitbucket.org/1.0/repositories/{owner}/{repo}/{0}", match, nodeType), &nodes); err != nil {
				return nil, nil, err
			}
			for t, n := range nodes {
				tags[t] = n.Node
			}
		}

		// Check revision tag.
		var err error
		match["tag"], match["commit"], err = bestTag(tags, defaultTags[match["vcs"]])
		if err != nil {
			return nil, nil, err
		}
	} else {
		match["commit"] = commit
	}

	// We use .tar.gz here.
	// zip : https://bitbucket.org/{owner}/{repo}/get/{commit}.zip
	// tarball : https://bitbucket.org/{owner}/{repo}/get/{commit}.tar.gz

	// Downlaod archive.
	p, err := httpGetBytes(client, expand("https://bitbucket.org/{owner}/{repo}/get/{commit}.tar.gz", match), nil)
	if err != nil {
		return nil, nil, err
	}

	installPath := installGOPATH + "/src/" + match["importPath"]

	// Remove old files.
	os.RemoveAll(installPath + "/")
	// Create destination directory.
	os.MkdirAll(installPath+"/", os.ModePerm)

	gzr, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return nil, nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var autoPath string // Auto path is the root path that generated by bitbucket.org.
	// Get source file data.
	dirs := make([]string, 0, 5)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, err
		}

		fn := h.FileInfo().Name()

		// In case that we find directory, usually we should not.
		if strings.HasSuffix(fn, "/") {
			continue
		}

		// Check root path.
		if len(autoPath) == 0 {
			autoPath = fn[:strings.Index(fn, "/")]
		}
		absPath := strings.Replace(fn, autoPath, installPath, 1)

		// Create diretory before create file.
		dir := path.Dir(absPath)
		if !checkDir(dir, dirs) && !(!cmdFlags["-e"] && strings.Contains(absPath, "example")) {
			dirs = append(dirs, dir)
			os.MkdirAll(dir+"/", os.ModePerm)
		}

		// Get data from archive.
		fbytes := make([]byte, h.Size)
		if _, err := io.ReadFull(tr, fbytes); err != nil {
			return nil, nil, err
		}

		// Write data to file
		fw, err := os.Create(absPath)
		if err != nil {
			return nil, nil, err
		}

		_, err = fw.Write(fbytes)
		fw.Close()
		if err != nil {
			return nil, nil, err
		}
	}

	pkg := &Package{
		ImportPath: match["importPath"],
		AbsPath:    installPath,
		Commit:     commit,
	}

	var imports []string

	// Check if need to check imports.
	if isCheckImport {
		for _, d := range dirs {
			imports, err = checkImports(d+"/", match["importPath"])
			if err != nil {
				return nil, nil, err
			}
		}
	}

	return pkg, imports, err
}

// checkDir checks if current directory has been saved.
func checkDir(dir string, dirs []string) bool {
	for _, d := range dirs {
		if dir == d {
			return true
		}
	}
	return false
}
