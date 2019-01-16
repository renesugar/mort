package object

import (
	"bytes"
	"encoding/hex"
	"errors"
	"github.com/aldor007/mort/pkg/config"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

var bufPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// ParseFnc is a function that create object from request url
type ParseFnc func(url *url.URL, bucketConfig config.Bucket, obj *FileObject) (string, error)

// parser list of available decoder function
var parsers = make(map[string]ParseFnc)

// Parse pare given url using appropriate parser
// it set object Key, Bucket, Parent and transforms
func Parse(url *url.URL, mortConfig *config.Config, obj *FileObject) error {
	elements := strings.SplitN(url.Path, "/", 3)
	lenElements := len(elements)
	if lenElements < 2 {
		return errors.New("invalid path " + url.Path)
	}

	obj.Bucket = elements[1]
	if lenElements > 2 {
		obj.Key = "/" + elements[2]
		obj.key = elements[2]
	}

	var parent string
	if bucketConfig, ok := mortConfig.Buckets[obj.Bucket]; ok {
		var err error
		var parentObj *FileObject
		if bucketConfig.Transform != nil {
			if fn, ok := parsers[bucketConfig.Transform.Kind]; ok {
				parent, err = fn(url, bucketConfig, obj)
			}

			if err != nil {
				return err
			}

			if parent == "" {
				obj.Storage = bucketConfig.Storages.Basic()
				return err
			}

			parentObj, err = NewFileObjectFromPath(parent, mortConfig)
			parentObj.Storage = bucketConfig.Storages.Get(bucketConfig.Transform.ParentStorage)

			obj.Parent = parentObj
			obj.CheckParent = bucketConfig.Transform.CheckParent
			if obj.Transforms.NotEmpty {
				obj.Storage = bucketConfig.Storages.Transform()
				switch bucketConfig.Transform.ResultKey {
				case "hash":
					obj.Key = hashKey(obj)
				case "hashParent":
					obj.Key = hashKeyParent(obj)
				}
			} else {
				obj.Storage = bucketConfig.Storages.Basic()
			}
		} else {
			obj.Storage = bucketConfig.Storages.Basic()
		}

		return err

	}

	return errors.New("unknown bucket")
}

func hashKey(obj *FileObject) string {
	hashB := []byte(strconv.FormatUint(uint64(obj.Transforms.Hash().Sum64()), 16))
	buf := bufPool.Get().(*bytes.Buffer)
	safePath := strings.Replace(obj.Parent.key, "/", "-", -1)
	sliceRange := 3

	if l := len(safePath); l < 3 {
		sliceRange = l
	}

	buf.Reset()
	buf.WriteByte('/')
	buf.Write(hashB[0:3])
	buf.WriteByte('/')
	buf.WriteString(safePath[0:sliceRange])
	buf.WriteByte('/')
	buf.WriteString(safePath)
	buf.WriteByte('-')
	buf.Write(hashB)
	bufPool.Put(buf)
	return buf.String()
}

func hashKeyParent(obj *FileObject) string {
	var currObj *FileObject
	currObj = obj.Parent
	transHash := currObj.Transforms.Hash()
	buf := bufPool.Get().(*bytes.Buffer)
	for currObj.HasParent() {
		buf.Reset()
		hSum := currObj.Transforms.Hash().Sum(nil)
		buf.WriteString(currObj.Key)
		buf.Write(hSum)
		transHash.Sum(buf.Bytes())
		currObj = currObj.Parent
	}
	hashB := transHash.Sum(nil)
	safePath := strings.Replace(currObj.key, "/", "-", -1)

	buf.Reset()
	buf.WriteByte('/')
	buf.WriteString(safePath)
	buf.WriteByte('/')
	buf.WriteString(hex.EncodeToString(hashB))
	bufPool.Put(buf)
	return buf.String()
}

// RegisterParser add new kind of function to map of decoders and for config validator
func RegisterParser(kind string, fn ParseFnc) {
	parsers[kind] = fn
	config.RegisterTransformKind(kind)
}
