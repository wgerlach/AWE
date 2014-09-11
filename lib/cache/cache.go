package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MG-RAST/AWE/lib/conf"
	"github.com/MG-RAST/AWE/lib/core"
	"github.com/MG-RAST/AWE/lib/httpclient"
	"github.com/MG-RAST/AWE/lib/logger"
	"github.com/MG-RAST/AWE/lib/logger/event"
	"github.com/MG-RAST/AWE/lib/shock"
	"io"
	"io/ioutil"
	"os"
	"time"
)

func UploadOutputData(work *core.Workunit) (size int64, err error) {
	for name, io := range work.Outputs {
		var local_filepath string //local file name generated by the cmd
		var file_path string      //file name to be uploaded to shock

		if io.Directory != "" {
			local_filepath = fmt.Sprintf("%s/%s/%s", work.Path(), io.Directory, name)
			//if specified, rename the local file name to the specified shock node file name
			//otherwise use the local name as shock file name
			file_path = local_filepath
			if io.ShockFilename != "" {
				file_path = fmt.Sprintf("%s/%s/%s", work.Path(), io.Directory, io.ShockFilename)
				os.Rename(local_filepath, file_path)
			}
		} else {
			local_filepath = fmt.Sprintf("%s/%s", work.Path(), name)
			file_path = local_filepath
			if io.ShockFilename != "" {
				file_path = fmt.Sprintf("%s/%s", work.Path(), io.ShockFilename)
				os.Rename(local_filepath, file_path)
			}
		}

		if (io.Type == "copy") || (io.Type == "update") || io.NoFile {
			file_path = ""
		} else if fi, err := os.Stat(file_path); err != nil {
			//skip this output if missing file and optional
			if io.Optional {
				continue
			} else {
				return size, errors.New(fmt.Sprintf("output %s not generated for workunit %s", name, work.Id))
			}
		} else {
			if io.Nonzero && fi.Size() == 0 {
				return size, errors.New(fmt.Sprintf("workunit %s generated zero-sized output %s while non-zero-sized file required", work.Id, name))
			}
			size += fi.Size()
		}

		logger.Debug(2, "deliverer: push output to shock, filename="+name)
		logger.Event(event.FILE_OUT,
			"workid="+work.Id,
			"filename="+name,
			fmt.Sprintf("url=%s/node/%s", io.Host, io.Node))

		//upload attribute file to shock IF attribute file is specified in outputs AND it is found in local directory.
		var attrfile_path string = ""
		if io.AttrFile != "" {
			attrfile_path = fmt.Sprintf("%s/%s", work.Path(), io.AttrFile)
			if fi, err := os.Stat(attrfile_path); err != nil || fi.Size() == 0 {
				attrfile_path = ""
			}
		}

		//set io.FormOptions["parent_node"] if not present and io.FormOptions["parent_name"] exists
		if parent_name, ok := io.FormOptions["parent_name"]; ok {
			for in_name, in_io := range work.Inputs {
				if in_name == parent_name {
					io.FormOptions["parent_node"] = in_io.Node
				}
			}
		}

		logger.Debug(1, "UploadOutputData, core.PutFileToShock: "+file_path)
		if err := core.PutFileToShock(file_path, io.Host, io.Node, work.Rank, work.Info.DataToken, attrfile_path, io.Type, io.FormOptions, io.NodeAttr); err != nil {

			time.Sleep(3 * time.Second) //wait for 3 seconds and try again
			if err := core.PutFileToShock(file_path, io.Host, io.Node, work.Rank, work.Info.DataToken, attrfile_path, io.Type, io.FormOptions, io.NodeAttr); err != nil {
				fmt.Errorf("push file error\n")
				logger.Error("op=pushfile,err=" + err.Error())
				return size, err
			}
		}
		logger.Event(event.FILE_DONE,
			"workid="+work.Id,
			"filename="+name,
			fmt.Sprintf("url=%s/node/%s", io.Host, io.Node))

		if io.ShockIndex != "" {
			if err := core.ShockPutIndex(io.Host, io.Node, io.ShockIndex, work.Info.DataToken); err != nil {
				logger.Error("warning: fail to create index on shock for shock node: " + io.Node)
			}
		}

		if conf.CACHE_ENABLED {
			//move output files to cache
			cacheDir := getCacheDir(io.Node)
			if err := os.MkdirAll(cacheDir, 0777); err != nil {
				logger.Error("cache os.MkdirAll():" + err.Error())
			}
			cacheFilePath := getCacheFilePath(io.Node) //use the same naming mechanism used by shock server
			//fmt.Printf("moving file from %s to %s\n", file_path, cacheFilePath)
			if err := os.Rename(file_path, cacheFilePath); err != nil {
				logger.Error("cache os.Rename():" + err.Error())
			}
		}
	}
	return
}

func getCacheDir(id string) string {
	if len(id) < 7 {
		return conf.DATA_PATH
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s", conf.DATA_PATH, id[0:2], id[2:4], id[4:6], id)
}

func getCacheFilePath(id string) string {
	cacheDir := getCacheDir(id)
	return fmt.Sprintf("%s/%s.data", cacheDir, id)
}

func StatCacheFilePath(id string) (file_path string, err error) {
	file_path = getCacheFilePath(id)
	_, err = os.Stat(file_path)
	return file_path, err
}

//fetch input data
func MoveInputData(work *core.Workunit) (size int64, err error) {
	for inputname, io := range work.Inputs {

		// skip if NoFile == true
		if !io.NoFile { // is file !
			var dataUrl string
			inputFilePath := fmt.Sprintf("%s/%s", work.Path(), inputname)

			if work.Rank == 0 {
				if conf.CACHE_ENABLED && io.Node != "" {
					if file_path, err := StatCacheFilePath(io.Node); err == nil {
						//make a link in work dir from cached file
						linkname := fmt.Sprintf("%s/%s", work.Path(), inputname)
						fmt.Printf("input found in cache, making link: " + file_path + " -> " + linkname + "\n")
						err = os.Symlink(file_path, linkname)
						if err == nil {
							logger.Event(event.FILE_READY, "workid="+work.Id+";url="+dataUrl)
						}
						return 0, err
					}
				}
				dataUrl = io.DataUrl()
			} else {
				dataUrl = fmt.Sprintf("%s&index=%s&part=%s", io.DataUrl(), work.IndexType(), work.Part())
			}
			logger.Debug(2, "mover: fetching input file from url:"+dataUrl)
			logger.Event(event.FILE_IN, "workid="+work.Id+";url="+dataUrl)

			// download file
			if datamoved, err := shock.FetchFile(inputFilePath, dataUrl, work.Info.DataToken, io.Uncompress); err != nil {
				return size, err
			} else {
				size += datamoved
			}
			logger.Event(event.FILE_READY, "workid="+work.Id+";url="+dataUrl)
		}

		// download node attributes if requested
		if io.AttrFile != "" {
			// get node
			node, err := shock.ShockGet(io.Host, io.Node, work.Info.DataToken)
			if err != nil {
				return size, err
			}
			logger.Debug(2, "mover: fetching input attributes from node:"+node.Id)
			logger.Event(event.ATTR_IN, "workid="+work.Id+";node="+node.Id)
			// print node attributes
			attrFilePath := fmt.Sprintf("%s/%s", work.Path(), io.AttrFile)
			attr_json, _ := json.Marshal(node.Attributes)
			if err := ioutil.WriteFile(attrFilePath, attr_json, 0644); err != nil {
				return size, err
			}
			logger.Event(event.ATTR_READY, "workid="+work.Id+";path="+attrFilePath)
		}
	}
	return
}

func isFileExistingInCache(id string) bool {
	file_path := getCacheFilePath(id)
	if _, err := os.Stat(file_path); err == nil {
		return true
	}
	return false
}

//fetch file by shock url TODO deprecated
func fetchFile_deprecated(filename string, url string, token string) (size int64, err error) {
	fmt.Printf("fetching file name=%s, url=%s\n", filename, url)
	localfile, err := os.Create(filename)
	if err != nil {
		return 0, err
	}
	defer localfile.Close()

	var user *httpclient.Auth
	if token != "" {
		user = httpclient.GetUserByTokenAuth(token)
	}

	//download file from Shock
	res, err := httpclient.Get(url, httpclient.Header{}, nil, user)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()

	if res.StatusCode != 200 { //err in fetching data
		resbody, _ := ioutil.ReadAll(res.Body)
		msg := fmt.Sprintf("op=fetchFile, url=%s, res=%s", url, resbody)
		return 0, errors.New(msg)
	}

	size, err = io.Copy(localfile, res.Body)
	if err != nil {
		return 0, err
	}
	return
}
