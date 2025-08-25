package gist

import (
	"github.com/google/uuid"
	"github.com/thomiceli/opengist/internal/db"
	"github.com/thomiceli/opengist/internal/i18n"
	"github.com/thomiceli/opengist/internal/validator"
	"github.com/thomiceli/opengist/internal/web/context"
	"github.com/rs/zerolog/log"
	"net/url"
	"strconv"
	"strings"
	"os"
	"os/exec"
	"fmt"
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

		// Write content to a temp file
		filePath := fmt.Sprintf("%s/%s", tmpDir, gist.PreviewFilename)
		err = os.WriteFile(filePath, []byte(dto.Files[0].Content), 0644)
		if err != nil {
			log.Printf("❌ Failed to write temp file for Gitleaks: %v", err)
			return ctx.ErrorRes(500, "Error writing temp file for Gitleaks", err)
		}

		// Run TruffleHog CLI scan
		cmd := exec.Command("trufflehog", "file", filePath, "--json")
		output, err := cmd.CombinedOutput()
		outputStr := string(output)

		if err != nil {
			// TruffleHog returns exit code 1 when secrets are found
			if _, ok := err.(*exec.ExitError); ok {
				log.Printf("🚨 TruffleHog detected sensitive content:\n%s", outputStr)
				return ctx.ErrorRes(400, "Gist contains sensitive information", fmt.Errorf("TruffleHog detected sensitive content: %s", outputStr))
			}

			// Actual execution error
			log.Printf("❌ TruffleHog execution error: %v\n%s", err, outputStr)
			return ctx.ErrorRes(500, "Error executing TruffleHog", err)
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
