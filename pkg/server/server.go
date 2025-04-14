// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/GoogleCloudPlatform/khi/pkg/common/filter"
	"github.com/GoogleCloudPlatform/khi/pkg/inspection"
	"github.com/GoogleCloudPlatform/khi/pkg/inspection/metadata"
	"github.com/GoogleCloudPlatform/khi/pkg/inspection/task"
	"github.com/GoogleCloudPlatform/khi/pkg/parameters"
	"github.com/GoogleCloudPlatform/khi/pkg/popup"
	"github.com/GoogleCloudPlatform/khi/pkg/server/config"
	"github.com/GoogleCloudPlatform/khi/pkg/server/upload"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

type ServerConfig struct {
	ViewerMode       bool
	StaticFolderPath string
	ResourceMonitor  ResourceMonitor
	ServerBasePath   string
	UploadFileStore  *upload.UploadFileStore
}

func redirectMiddleware(exactPath string, redirectTo string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.Path == exactPath {
			ctx.Redirect(302, redirectTo)
			return
		}
		ctx.Next()
	}
}

func CreateKHIServer(inspectionServer *inspection.InspectionTaskServer, serverConfig *ServerConfig) *gin.Engine {
	engine := instanciateGinServer(parameters.Debug.Verbose != nil && *parameters.Debug.Verbose)
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true

	appHtmlPath := path.Join(serverConfig.StaticFolderPath, "/index.html")

	basePathWithoutTrailingSlash := strings.TrimSuffix(serverConfig.ServerBasePath, "/")
	engine.Use(redirectMiddleware(basePathWithoutTrailingSlash+"/", basePathWithoutTrailingSlash+"/session/0")) // Request for `/` shouldn't be handled by `static.Serve`, redirect `/session/0` to be handled by patternToString
	engine.Use(static.Serve(basePathWithoutTrailingSlash+"/", static.LocalFile(serverConfig.StaticFolderPath, false)))
	engine.Use(gin.Recovery())
	engine.Use(cors.New(corsConfig))
	router := engine.Group(basePathWithoutTrailingSlash)

	// frontend uses Angular router. All frontend routing path should return the app html
	router.GET("/session/*wild", func(ctx *gin.Context) {
		ctx.Header("Content-Type", "text/html")
		file, err := os.ReadFile(appHtmlPath)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}
		originalIndexHTML := string(file)
		replacedIndexHtml, err := replaceDynamicPartOfIndex(originalIndexHTML)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}
		ctx.Writer.Write([]byte(replacedIndexHtml))
	})
	// GET /api/v2/config
	// Returns configuration map used in frontend.
	router.GET("/api/v2/config", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, config.NewGetConfigResponseFromParameters())
	})

	if !serverConfig.ViewerMode {
		// GET /api/v2/inspection/types
		// Returns the list of inspection types available on the inspection server.
		router.GET("/api/v2/inspection/types", func(ctx *gin.Context) {
			ctx.JSON(http.StatusOK, &GetInspectionTypesResponse{
				Types: inspectionServer.GetAllInspectionTypes(),
			})
		})

		// GET /api/v2/inspection/tasks
		// Returns the all started inspections on the inspection server.
		router.GET("/api/v2/inspection/tasks", func(ctx *gin.Context) {
			inspections := inspectionServer.GetAllRunners()
			responseInspections := map[string]SerializedMetadata{}
			for _, inspection := range inspections {
				if inspection.Started() {
					md, err := inspection.GetCurrentMetadata()
					if err != nil {
						ctx.String(http.StatusInternalServerError, err.Error())
						return
					}

					m, err := metadata.GetSerializableSubsetMapFromMetadataSet(md, filter.NewEnabledFilter(metadata.LabelKeyIncludedInTaskListFlag, false))
					if err != nil {
						ctx.String(http.StatusInternalServerError, err.Error())
						return
					}
					responseInspections[inspection.ID] = m
				}
			}

			ctx.JSON(http.StatusOK, &GetInspectionTasksResponse{
				Tasks: responseInspections,
				ServerStat: &ServerStat{
					TotalMemoryAvailable: serverConfig.ResourceMonitor.GetUsedMemory(),
				},
			})
		})

		// POST /api/v2/inspection/tasks
		router.POST("/api/v2/inspection/types/:typeId", func(ctx *gin.Context) {
			typeId := ctx.Param("typeId")
			inspectionId, err := inspectionServer.CreateInspection(typeId)
			if err != nil {
				// only the not found error is expected here
				ctx.String(http.StatusNotFound, err.Error())
				return
			}
			ctx.JSON(http.StatusAccepted, &PostInspectionTaskResponse{InspectionId: inspectionId})
		})
		// PUT /api/v2/inspection/tasks/<task-id>/features
		router.PUT("/api/v2/inspection/tasks/:taskId/features", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			task := inspectionServer.GetTask(taskId)
			if task == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			var reqBody PutInspectionTaskFeatureRequest
			if err := ctx.ShouldBindJSON(&reqBody); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			err := task.SetFeatureList(reqBody.Features)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.String(http.StatusAccepted, "ok")
		})
		// PATCH /api/v2/inspection/tasks/<task-id>/features
		router.PATCH("/api/v2/inspection/tasks/:taskId/features", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			task := inspectionServer.GetTask(taskId)
			if task == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			var reqBody PatchInspectionTaskFeatureRequest
			if err := ctx.ShouldBindJSON(&reqBody); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			err := task.UpdateFeatureMap(reqBody.Features)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.String(http.StatusAccepted, "ok")
		})
		// GET /api/v2/inspection/tasks/<task-id>/features
		router.GET("/api/v2/inspection/tasks/:taskId/features", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			task := inspectionServer.GetTask(taskId)
			if task == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			features, err := task.FeatureList()
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.JSON(http.StatusOK, GetInspectionTaskFeatureResponse{
				Features: features,
			})
		})

		router.POST("/api/v2/inspection/tasks/:taskId/dryrun", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			currentTask := inspectionServer.GetTask(taskId)
			if currentTask == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			var reqBody PostInspectionTaskDryRunRequest
			if err := ctx.ShouldBindJSON(&reqBody); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			result, err := currentTask.DryRun(ctx, &task.InspectionRequest{
				Values: reqBody,
			})
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.JSON(http.StatusOK, result)
		})

		router.POST("/api/v2/inspection/tasks/:taskId/run", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			currentTask := inspectionServer.GetTask(taskId)
			if currentTask == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			var reqBody PostInspectionTaskDryRunRequest
			if err := ctx.ShouldBindJSON(&reqBody); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			err := currentTask.Run(ctx, &task.InspectionRequest{
				Values: reqBody,
			})
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.String(http.StatusAccepted, "ok")
		})

		router.POST("/api/v2/inspection/tasks/:taskId/cancel", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			currentTask := inspectionServer.GetTask(taskId)
			if currentTask == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			err := currentTask.Cancel()
			if err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			ctx.String(http.StatusOK, "ok")
		})

		router.GET("/api/v2/inspection/tasks/:taskId/metadata", func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			currentTask := inspectionServer.GetTask(taskId)
			if currentTask == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}
			result, err := currentTask.Metadata()
			if err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			ctx.JSON(http.StatusOK, result)
		})

		inspectionTaskDataHandler := func(ctx *gin.Context) {
			taskId := ctx.Param("taskId")
			currentTask := inspectionServer.GetTask(taskId)
			if currentTask == nil {
				ctx.String(http.StatusNotFound, fmt.Sprintf("task %s was not found", taskId))
				return
			}

			// parse range queries
			var rangeStart int64
			var maxSize int64 = math.MaxInt64
			startQueryStr := ctx.Query("start")
			maxSizeQueryStr := ctx.Query("maxSize")
			if startQueryStr != "" {
				var err error
				rangeStart, err = strconv.ParseInt(startQueryStr, 10, 64)
				if err != nil {
					ctx.String(http.StatusBadRequest, err.Error())
					return
				}
			}
			if maxSizeQueryStr != "" {
				var err error
				maxSize, err = strconv.ParseInt(maxSizeQueryStr, 10, 64)
				if err != nil {
					ctx.String(http.StatusBadRequest, err.Error())
					return
				}
			}

			result, err := currentTask.Result()
			if err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			inspectionDataReader, err := result.ResultStore.GetRangeReader(rangeStart, maxSize)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			fileSize, err := result.ResultStore.GetInspectionResultSizeInBytes()
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			contentType := "application/octet-stream"
			contentLength := int64(math.Min(float64(maxSize), float64(fileSize-int(rangeStart))))
			if ctx.Request.Method == http.MethodHead {
				ctx.Header("Content-Type", contentType)
				ctx.Header("Content-Length", strconv.Itoa(int(contentLength)))
				ctx.Status(http.StatusOK)
			} else {
				ctx.DataFromReader(http.StatusOK, contentLength, contentType, inspectionDataReader, map[string]string{})
			}
			result.ResultStore.Close()
		}
		router.HEAD("/api/v2/inspection/tasks/:taskId/data", inspectionTaskDataHandler)
		router.GET("/api/v2/inspection/tasks/:taskId/data", inspectionTaskDataHandler)

		router.GET("/api/v2/popup", func(ctx *gin.Context) {
			currentPopup := popup.Instance.GetCurrentPopup()
			if currentPopup == nil {
				ctx.String(http.StatusOK, "")
				return
			}
			ctx.JSON(http.StatusOK, currentPopup)
		})

		router.POST("/api/v2/popup/validate", func(ctx *gin.Context) {
			request := &popup.PopupAnswerResponse{}
			if err := ctx.ShouldBindJSON(request); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			result, err := popup.Instance.Validate(request)
			if errors.Is(err, popup.NoCurrentPopup) {
				ctx.String(http.StatusNotFound, err.Error())
				return
			}
			if errors.Is(err, popup.CurrentPopupIsntMatchingWithGivenId) {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.JSON(http.StatusOK, result)
		})

		router.POST("/api/v2/popup/answer", func(ctx *gin.Context) {
			request := &popup.PopupAnswerResponse{}
			if err := ctx.ShouldBindJSON(request); err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			err := popup.Instance.Answer(request)
			if errors.Is(err, popup.NoCurrentPopup) {
				ctx.String(http.StatusNotFound, err.Error())
				return
			}
			if errors.Is(err, popup.CurrentPopupIsntMatchingWithGivenId) {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			ctx.String(http.StatusOK, "")
		})

		router.POST("/api/v2/upload", func(ctx *gin.Context) {
			localUploadFileStoreProvider, convertible := serverConfig.UploadFileStore.StoreProvider.(*upload.LocalUploadFileStoreProvider)
			if !convertible {
				ctx.String(http.StatusBadRequest, "invalid operation. Current UploadFileStore.StoreProvider is not supporting to be written directly")
				return
			}
			file, err := ctx.FormFile("file")
			if err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}

			id := ctx.Request.FormValue("upload-token-id")
			if id == "" {
				ctx.String(http.StatusBadRequest, "missing upload-token-id")
				return
			}

			token := &upload.DirectUploadToken{ID: id}
			if parameters.Server.MaxUploadFileSizeInBytes != nil && *parameters.Server.MaxUploadFileSizeInBytes < int(file.Size) {
				ctx.String(http.StatusBadRequest, fmt.Sprintf("file size exceeds the limit (%d bytes)", *parameters.Server.MaxUploadFileSizeInBytes))
				return
			}

			err = serverConfig.UploadFileStore.SetResultOnStartingUpload(token)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}

			multipart, err := file.Open()
			if err != nil {
				ctx.String(http.StatusBadRequest, err.Error())
				return
			}
			defer multipart.Close()

			err = localUploadFileStoreProvider.Write(token, multipart)
			if err != nil {
				serverConfig.UploadFileStore.SetResultOnCompletedUpload(token, err)
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}
			serverConfig.UploadFileStore.SetResultOnCompletedUpload(token, nil)

			ctx.String(http.StatusOK, "")
		})
	}
	return engine
}

// instanciateGinServer generates a new instance of *gin.Engine with provided debug mode flag.
func instanciateGinServer(debugMode bool) *gin.Engine {
	if debugMode {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	if debugMode {
		engine.Use(gin.Logger())
	}
	return engine
}
