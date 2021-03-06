package handler

import (
	"bb-pan/src/meta"
	"bb-pan/src/util"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"bb-pan/src/store/oss"
	dblayer "bb-pan/src/db"
)

func UploadHandler(w http.ResponseWriter,r *http.Request){
	if r.Method == "GET"{
	data,err :=	ioutil.ReadFile("D:/go-work/bb-pan/src/static/view/index.html")
	if err != nil{
		io.WriteString(w,"internel server error:"+err.Error())
		return
	}
	io.WriteString(w,string(data))
	}else if r.Method == "POST"{

		file,head,err := r.FormFile("file")
		if err != nil{
			fmt.Printf("Failed to get data,err:%s\n",err.Error())
			return
		}

		defer file.Close()


		fileMeta := meta.FileMeta{
			FileName:head.Filename,
			Location:"D:/tmp/"+head.Filename,
			UploadAt:time.Now().Format("2006-01-02 15:04:05"),
		}

		newFile,err := os.Create(fileMeta.Location)

		if err != nil{
			fmt.Printf("Failed to create fle,err:%s\n",err)
			return
		}
		defer newFile.Close()

		fileMeta.FileSize,err = io.Copy(newFile,file)
		if err != nil{
			fmt.Printf("Failed to sae data into file,err:%s",err.Error())
			return
		}

		newFile.Seek(0,0)
		fileMeta.FileSha1 = util.FileSha1(newFile)

		//meta.UpdateFileMeta(fileMeta)

		// 游标重新回到文件头部
		newFile.Seek(0,0)
		// 文件写入Ceph存储
		//data, _ := ioutil.ReadAll(newFile)
		//cephPath := "/ceph/" + fileMeta.FileSha1
		//_ = ceph.PutObject("userfile", cephPath, data)
		//fileMeta.Location = cephPath


		// 文件写入OSS存储
		ossPath := "oss/" + fileMeta.FileSha1

		err = oss.Bucket().PutObject(ossPath, newFile)
		if err != nil {
			fmt.Println(err.Error())
			w.Write([]byte("Upload failed!"))
			return
		}
		fileMeta.Location = ossPath


		_ = meta.UpdateFileMetaDB(fileMeta)


		r.ParseForm()

		username := r.Form.Get("username")

		suc := dblayer.OnUserFileUploadFinished(username,fileMeta.FileSha1,fileMeta.FileName,fileMeta.FileSize)

		if suc{
			//上传成功跳转
			http.Redirect(w,r,"/file/upload/suc",http.StatusFound)
		}else{
			w.Write([]byte("Upload Filed"))
		}


	}
}

//上传完成操作
func UploadSucHandler(w http.ResponseWriter, r * http.Request){
	io.WriteString(w,"Upload finished")
}


func GetFileMetaHandler(w http.ResponseWriter,r *http.Request){
	r.ParseForm()

	filehash := r.Form["filehash"][0]
	//fMeta := meta.GetFileMeta(filehash)
	fMeta,err := meta.GetFileMetaDB(filehash)
	if err != nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	data,err := json.Marshal(fMeta)

	if err!=nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(data)


}


//下载
func DownloadHandler(w http.ResponseWriter,r *http.Request){
	r.ParseForm()
	fsha1 :=r.Form.Get("filehash")

	fm := meta.GetFileMeta(fsha1)

	f,err := os.Open(fm.Location)
	if err!=nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	data,err := ioutil.ReadAll(f)

	if err != nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type","application/octect-stream")
	w.Header().Set("content-disposition","attachment;filename=\""+fm.FileName+"\"")
	w.Write(data)

}


//修改文件元信息
func FileUpdateMetaHandler(w http.ResponseWriter, r *http.Request){
	r.ParseForm()

	opType := r.Form.Get("op")
	fileSha1 := r.Form.Get("filehash")
	newFileName := r.Form.Get("filename")

	if opType != "0"{
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if r.Method != "POST"{
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	curFileMeta := meta.GetFileMeta(fileSha1)
	curFileMeta.FileName = newFileName
	meta.UpdateFileMeta(curFileMeta)

	data,err := json.Marshal(curFileMeta)
	if err !=nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(data)

}


//删除文件
func FileDelHandler(w http.ResponseWriter, r *http.Request){
	r.ParseForm()

	fileSha1 := r.Form.Get("filehash")

	fMeta := meta.GetFileMeta(fileSha1)

	os.Remove(fMeta.Location)

	meta.RemoveFileMeta(fileSha1)
	w.WriteHeader(http.StatusOK)
}


// FileQueryHandler : 查询批量的文件元信息
func FileQueryHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	limitCnt, _ := strconv.Atoi(r.Form.Get("limit"))
	username := r.Form.Get("username")
	//fileMetas, _ := meta.GetLastFileMetasDB(limitCnt)
	userFiles, err := dblayer.QueryUserFileMetas(username, limitCnt)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(userFiles)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(data)
}


// TryFastUploadHandler : 尝试秒传接口
func TryFastUploadHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	// 1. 解析请求参数
	username := r.Form.Get("username")
	filehash := r.Form.Get("filehash")
	filename := r.Form.Get("filename")
	filesize, _ := strconv.Atoi(r.Form.Get("filesize"))

	// 2. 从文件表中查询相同hash的文件记录
	fileMeta, err := meta.GetFileMetaDB(filehash)
	if err != nil {
		fmt.Println(err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 3. 查不到记录则返回秒传失败
	if fileMeta == nil {
		resp := util.RespMsg{
			Code: -1,
			Msg:  "秒传失败，请访问普通上传接口",
		}
		w.Write(resp.JSONBytes())
		return
	}

	// 4. 上传过则将文件信息写入用户文件表， 返回成功
	suc := dblayer.OnUserFileUploadFinished(
		username, filehash, filename, int64(filesize))
	if suc {
		resp := util.RespMsg{
			Code: 0,
			Msg:  "秒传成功",
		}
		w.Write(resp.JSONBytes())
		return
	}
	resp := util.RespMsg{
		Code: -2,
		Msg:  "秒传失败，请稍后重试",
	}
	w.Write(resp.JSONBytes())
	return
}



// DownloadURLHandler : 生成文件的下载地址
func DownloadURLHandler(w http.ResponseWriter, r *http.Request) {
	filehash := r.Form.Get("filehash")
	// 从文件表查找记录
	row, _ := dblayer.GetFileMeta(filehash)

	// TODO: 判断文件存在OSS，还是Ceph，还是在本地
	if strings.HasPrefix(row.FileAddr.String, "/tmp") {
		username := r.Form.Get("username")
		token := r.Form.Get("token")
		tmpUrl := fmt.Sprintf("http://%s/file/download?filehash=%s&username=%s&token=%s",
			r.Host, filehash, username, token)
		w.Write([]byte(tmpUrl))
	} else if strings.HasPrefix(row.FileAddr.String, "/ceph") {
		// TODO: ceph下载url
	} else if strings.HasPrefix(row.FileAddr.String, "oss/") {
		// oss下载url
		signedURL := oss.DownloadURL(row.FileAddr.String)
		w.Write([]byte(signedURL))
	}
}


















