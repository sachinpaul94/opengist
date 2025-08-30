package gist

import (
	"github.com/google/uuid"
	"github.com/sachinpaul94/opengist/internal/db"
	"github.com/sachinpaul94/opengist/internal/i18n"
	"github.com/sachinpaul94/opengist/internal/validator"
	"github.com/sachinpaul94/opengist/internal/web/context"
	"github.com/sachinpaul94/opengist/internal/web/handlers"
	"github.com/rs/zerolog/log"
	"net/url"
	"strconv"
	"strings"
	"os"
	"os/exec"
	"fmt"
	stdctx "context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func Create(ctx *context.Context) error {
	ctx.SetData("htmlTitle", ctx.TrH("gist.new.create-a-new-gist"))
	return ctx.Html("create.html")
}

func ProcessCreate(ctx *context.Context) error {
	isCreate := false
	if ctx.Request().URL.Path == "/" {
		isCreate = true
	}

	err := ctx.Request().ParseForm()
	if err != nil {
		return ctx.ErrorRes(400, ctx.Tr("error.bad-request"), err)
	}

	dto := new(db.GistDTO)
	var gist *db.Gist

	if isCreate {
		ctx.SetData("htmlTitle", ctx.TrH("gist.new.create-a-new-gist"))
	} else {
		gist = ctx.GetData("gist").(*db.Gist)
		ctx.SetData("htmlTitle", ctx.TrH("gist.edit.edit-gist", gist.Title))
	}

	if err := ctx.Bind(dto); err != nil {
		return ctx.ErrorRes(400, ctx.Tr("error.cannot-bind-data"), err)
	}

	dto.Files = make([]db.FileDTO, 0)
	fileCounter := 0
	for i := 0; i < len(ctx.Request().PostForm["content"]); i++ {
		name := ctx.Request().PostForm["name"][i]
		content := ctx.Request().PostForm["content"][i]

		if name == "" {
			fileCounter += 1
			name = "gistfile" + strconv.Itoa(fileCounter) + ".txt"
		}

		escapedValue, err := url.PathUnescape(content)
		if err != nil {
			return ctx.ErrorRes(400, ctx.Tr("error.invalid-character-unescaped"), err)
		}

		dto.Files = append(dto.Files, db.FileDTO{
			Filename: strings.Trim(name, " "),
			Content:  escapedValue,
		})
	}
	ctx.SetData("dto", dto)

	err = ctx.Validate(dto)
	if err != nil {
		ctx.AddFlash(validator.ValidationMessages(&err, ctx.GetData("locale").(*i18n.Locale)), "error")
		if isCreate {
			return ctx.HtmlWithCode(400, "create.html")
		} else {
			files, err := gist.Files("HEAD", false)
			if err != nil {
				return ctx.ErrorRes(500, "Error fetching files", err)
			}
			ctx.SetData("files", files)
			return ctx.HtmlWithCode(400, "edit.html")
		}
	}

	if isCreate {
		gist = dto.ToGist()
	} else {
		gist = dto.ToExistingGist(gist)
	}

	user := ctx.User
	gist.NbFiles = len(dto.Files)

	if isCreate {
		uuidGist, err := uuid.NewRandom()
		if err != nil {
			return ctx.ErrorRes(500, "Error creating an UUID", err)
		}
		gist.Uuid = strings.Replace(uuidGist.String(), "-", "", -1)

		gist.UserID = user.ID
		gist.User = *user
	}

	if gist.Title == "" {
		if ctx.Request().PostForm["name"][0] == "" {
			gist.Title = "gist:" + gist.Uuid
		} else {
			gist.Title = ctx.Request().PostForm["name"][0]
		}
	}

	if len(dto.Files) > 0 {
		split := strings.Split(dto.Files[0].Content, "\n")
		if len(split) > 10 {
			gist.Preview = strings.Join(split[:10], "\n")
		} else {
			gist.Preview = dto.Files[0].Content
		}

		gist.PreviewFilename = dto.Files[0].Filename

		// Create a unique temp directory
		tmpDir, err := os.MkdirTemp("", "gistscan-*")
		if err != nil {
			log.Printf("❌ Failed to create temp dir for Gitleaks: %v", err)
			return ctx.ErrorRes(500, "Error creating temp directory for Gitleaks", err)
		}
		defer os.RemoveAll(tmpDir) // clean up after scan

		// Upload content to S3 instead of writing to local filesystem
		s3Bucket := os.Getenv("S3_BUCKET") // Set your bucket name in env
		s3Key := fmt.Sprintf("gists/%s/%s", gist.Uuid, gist.PreviewFilename)

		awsCfg, err := config.LoadDefaultConfig(stdctx.Background())
		if err != nil {
			log.Printf("❌ Failed to load AWS config: %v", err)
			return ctx.ErrorRes(500, "Error loading AWS config for S3", err)
		}
		s3Client := s3.NewFromConfig(awsCfg)
		_, err = s3Client.PutObject(stdctx.Background(), &s3.PutObjectInput{
			Bucket: aws.String(s3Bucket),
			Key:    aws.String(s3Key),
			Body:   strings.NewReader(dto.Files[0].Content),
			ContentType: aws.String("text/plain"),
			ACL:    types.ObjectCannedACLPublicRead, // adjust as needed
		})
		if err != nil {
			log.Printf("s3 Bucket: %s, Key: %s ||| ACL: %s", s3Bucket, s3Key, types.ObjectCannedACLPublicRead)
			log.Printf("❌ Failed to upload file to S3: %v", err)
			return ctx.ErrorRes(500, "Error uploading file to S3", err)
		}

		// Download file from S3 for TruffleHog scan
		getObjOut, err := s3Client.GetObject(stdctx.Background(), &s3.GetObjectInput{
			Bucket: aws.String(s3Bucket),
			Key:    aws.String(s3Key),
		})
		if err != nil {
			log.Printf("❌ Failed to download file from S3 for scan: %v", err)
			return ctx.ErrorRes(500, "Error downloading file from S3 for scan", err)
		}
		defer getObjOut.Body.Close()

		// Run TruffleHog CLI scan with updater disabled, using stdin
		cmd := exec.Command("trufflehog", "stdin", "--json", "--no-update")
		cmd.Stdin = getObjOut.Body
		output, err := cmd.CombinedOutput()
		outputStr := string(output)

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				// TruffleHog found secrets, treat as 400
				if err := handlers.ValidateTruffleHogOutput(output); err != nil {
					log.Printf("🚨 %v", err)
					return ctx.ErrorRes(400, "Gist contains sensitive information", err)
				}
			} else {
				// Other errors (scan failure)
				return ctx.ErrorRes(500, "TruffleHog scan error", err)
			}
		} else {
			// No error, but still check output for secrets
			if err := handlers.ValidateTruffleHogOutput(output); err != nil {
				log.Printf("🚨 %v", err)
				return ctx.ErrorRes(400, "Gist contains sensitive information", err)
			}
		}

		if err := handlers.ValidateTruffleHogOutput(output); err != nil {
			log.Printf("🚨 %v", err)
			return ctx.ErrorRes(400, "Gist contains sensitive information", err)
		}

		log.Printf("✅ TruffleHog scan passed cleanly:\n%s", outputStr)
	}

	if err = gist.InitRepository(); err != nil {
		return ctx.ErrorRes(500, "Error creating the repository", err)
	}

	if err = gist.AddAndCommitFiles(&dto.Files); err != nil {
		return ctx.ErrorRes(500, "Error adding and committing files", err)
	}

	if isCreate {
		if err = gist.Create(); err != nil {
			return ctx.ErrorRes(500, "Error creating the gist", err)
		}
	} else {
		if err = gist.Update(); err != nil {
			return ctx.ErrorRes(500, "Error updating the gist", err)
		}
	}

	gist.AddInIndex()
	gist.UpdateLanguages()

	return ctx.RedirectTo("/" + user.Username + "/" + gist.Identifier())
}
