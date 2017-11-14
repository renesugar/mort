package middleware

import (
	"net/http"
	//"github.com/crunchytom/go-aws-auth"
	//awsv4 "githubBucketcom/aws/aws-sdk-go/aws/signer/v4"
	"regexp"
	"strings"
	"encoding/xml"
	"time"
	"fmt"

	"mort/config"
	"mort/response"

	"github.com/aldor007/go-aws-auth"
)

var AutHeaderRegexpv4 = regexp.MustCompile("^(:?[A-Za-z0-9-]+) Credential=(:?.+),\\s*SignedHeaders=(:?[a-zA-Z0-9;-]+),\\s*Signature=(:?[a-zA-Z0-9]+)$")
var AuthHeaderRegexpv2 = regexp.MustCompile("^AWS ([A-Za-z0-9-]+):(.+)$")

func isAuthRequired(method string, auth string, path string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		if path == "/" {
			return true
		}

		if auth != "" {
			return true
		}

		return false
	case "POST", "PUT", "DELETE", "PATCH":
		return true
	}

	return true
}

type s3Auth struct {
	mortConfig *config.Config
}


// NewS3AuthDiddleware returns S3 compatible authorization handler
// Correctly it can handle AWS v2 (S3 mode) and AWS v4 (only header mode without streaming)
func NewS3AuthMiddleware(mortConfig *config.Config) *s3Auth  {
	return &s3Auth{mortConfig:mortConfig}
}

func (s * s3Auth) Handler(next http.Handler) http.Handler {
	mortConfig := s.mortConfig
	fn := func(resWriter http.ResponseWriter, req *http.Request)  {
		path := req.URL.Path
		auth := req.Header.Get("Authorization")
		if !isAuthRequired(req.Method, auth, path) {
			next.ServeHTTP(resWriter, req)
			return
		}

		pathSlice := strings.Split(path, "/")
		pathSliceLen := len(pathSlice)
		if pathSliceLen < 2 {
			res := response.NewString(400, "invalid path")
			res.Send(resWriter)
			return
		}

		bucketName := pathSlice[1]


		var accessKey string
		var signedHeaders []string
		var bucket config.Bucket
		var credential awsauth.Credentials
		var authAlg string

		matches := AutHeaderRegexpv4.FindStringSubmatch(auth)
		if len(matches) == 5 {
			authAlg = "v4"
			alg := matches[1]
			if alg != "AWS4-HMAC-SHA256" {
				res := response.NewString(400, "invalid algorithm")
				res.Send(resWriter)
				return
			}

			reqCredField := matches[2]
			accessKey = strings.Split(reqCredField, "/")[0]
			signedHeaders = strings.Split(matches[3], ";")
		}

		matches = AuthHeaderRegexpv2.FindStringSubmatch(auth)
		if len(matches) == 3 {
			authAlg = "s3"
			accessKey = matches[1]
		}

		bucket, ok := mortConfig.Buckets[bucketName]
		if !ok {
			buckets := mortConfig.BucketsByAccessKey(accessKey)
			if len(buckets) == 0 {
				res := response.NewString(403, "")
				res.Send(resWriter)
				return
			}

			bucket = buckets[0]
		}

		keys := bucket.Keys
		for _, key := range keys {
			if accessKey == key.AccessKey {
				credential.AccessKeyID = accessKey
				credential.SecretAccessKey = key.SecretAccessKey
				break
			}

		}
		if credential.AccessKeyID == "" {
			res := response.NewString(401, "")
			res.Send(resWriter)
			return
		}

		validiatonReq, err := http.NewRequest(req.Method, req.RequestURI, req.Body)
		if err != nil {
			res := response.NewString(401, "")
			res.Send(resWriter)
			return
		}

		for h, v := range req.Header {
			if strings.HasPrefix(strings.ToLower(h),"x-amz")  {
				validiatonReq.Header.Set(h, v[0])
			}

			switch h {
			case "Content-Type", "Content-Md5", "Host", "Date":
				validiatonReq.Header.Set(h, v[0])
			}
		}

		for _, h := range signedHeaders {
			validiatonReq.Header.Set(h, req.Header.Get(h))
		}

		// FIXME: there will be problem with escaped paths
		validiatonReq.URL = req.URL
		validiatonReq.Method = req.Method
		validiatonReq.Body = req.Body
		validiatonReq.Host = req.Host

		if authAlg == "s3" {
			awsauth.SignS3(validiatonReq,  credential)
		} else {
			awsauth.Sign4ForRegion(validiatonReq, "mort", "s3", credential)
		}


		if auth == validiatonReq.Header.Get("Authorization") {
			req.Body = validiatonReq.Body
			//c.Set("accessKey", accessKey)
			if path == "/"  {
				s.listAllMyBuckets(resWriter, accessKey)
				return
			}
			next.ServeHTTP(resWriter, req)
			return

		}

		fmt.Printf("auth = %s valid = %s", auth, validiatonReq.Header.Get("Authorization"))
		response.NewString(403, "").Send(resWriter)
		return
	}

	return http.HandlerFunc(fn)
}


func (s *s3Auth) listAllMyBuckets(resWriter http.ResponseWriter, accessKey string)  {
	type bucketXml struct {
		XMLName     xml.Name `xml:"Bucket"`
		Name string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`

	}

	type listAllBucketsResult struct {
		XMLName     xml.Name `xml:"ListAllMyBucketsResult"`
		Owner      struct {
			ID     string `xml:"ID"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"owner"`
		Buckets []bucketXml `xml:"Buckets>Bucket"`
	}

	buckets := s.mortConfig.BucketsByAccessKey(accessKey)
	listAllBucketsXML := listAllBucketsResult{}
	listAllBucketsXML.Owner.DisplayName = accessKey
	listAllBucketsXML.Owner.ID = accessKey
	
	for _, bucket := range buckets {
		b := bucketXml{}
		b.Name = bucket.Name
		b.CreationDate = time.Now().Format(time.RFC3339)
		listAllBucketsXML.Buckets = append(listAllBucketsXML.Buckets, b)
	}

	b, err := xml.Marshal(listAllBucketsXML)
	if err != nil {
		res := response.NewError(500, err)
		res.Send(resWriter)
		return
	}

	res := response.NewBuf(200, b)
	res.SetContentType("application/xml")
	res.Send(resWriter)
}
