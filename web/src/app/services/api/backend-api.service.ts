/**
 * Copyright 2024 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { Injectable } from '@angular/core';
import {
  GetInspectionTypesResponse,
  CreateInspectionTaskResponse,
  GetInspectionTaskFeatureResponse,
  PatchInspectionTaskFeatureRequest,
  InspectionFeature,
  InspectionDryRunResponse,
  GetInspectionTasksResponse,
  InspectionDryRunRequest,
  InspectionRunRequest,
  PopupAnswerResponse,
  PopupAnswerValidationResult,
  PopupFormRequest,
  InspectionMetadataOfRunResult,
  GetConfigResponse,
} from '../../common/schema/api-types';
import {
  HttpClient,
  HttpEvent,
} from '@angular/common/http';
import {
  Observable,
  ReplaySubject,
  Subject,
  concat,
  debounceTime,
  forkJoin,
  map,
  mergeMap,
  of,
  range,
  reduce,
  retry,
  shareReplay,
  switchMap,
  withLatestFrom,
} from 'rxjs';
import { ViewStateService } from '../view-state.service';
import { BackendAPI, DownloadProgressReporter } from './backend-api-interface';
import { ProgressDialogStatusUpdator } from '../progress/progress-interface';
import { ProgressUtil } from '../progress/progress-util';
import { UploadToken } from 'src/app/common/schema/form-types';

/**
 * An implementation of BackendAPI interface.
 * All of the actual request calls against the backend must be through this class.
 */
@Injectable({
  providedIn: 'root',
})
export class BackendAPIImpl implements BackendAPI {
  private readonly MAX_INSPECTION_DATA_DOWNLOAD_CHUNK_SIZE = 1 * 1024 * 1024;
  private readonly DATA_DOWNLOAD_CONCURRENCY = 10;

  /**
   * The base address of the backend server.
   *
   * The index HTML file contains `<base>` tag to control the base address of resources in frontend to supporting KHI to be hosted with path rewriting behind reverse proxies.
   */
  private readonly baseUrl: string;

  private readonly getConfigObservable: Observable<GetConfigResponse>;

  constructor(
    private http: HttpClient,
    private readonly viewState: ViewStateService,
  ) {
    this.baseUrl = BackendAPIImpl.getServerBasePath();

    const getConfigUrl = this.baseUrl + '/api/v2/config';
    this.getConfigObservable = this.http
      .get<GetConfigResponse>(getConfigUrl)
      .pipe(
        retry({ delay: 1000 }),
        shareReplay(1), // the config is cached at the first time of the loading.
      );
  }

  /**
   * Get the server base path configuration path which is a configuration given as meta tag from backend.
   */
  public static getServerBasePath(): string {
    const basePathTag = document.getElementById('server-base-path');
    if (basePathTag === null) return '';
    let content = basePathTag.getAttribute('content');
    if (content?.endsWith('/')) {
      content = content.substring(0, content.length - 1);
    }
    return content ?? '';
  }

  /**
   * Get configuration of this frontend from the server.
   */
  public getConfig(): Observable<GetConfigResponse> {
    return this.getConfigObservable;
  }

  public getInspectionTypes() {
    const url = this.baseUrl + '/api/v2/inspection/types';
    return this.http.get<GetInspectionTypesResponse>(url);
  }

  public getTaskStatuses() {
    const url = this.baseUrl + '/api/v2/inspection/tasks';
    return this.http.get<GetInspectionTasksResponse>(url);
  }

  public createInspection(
    inspectionTypeId: string,
  ): Observable<InspectionTaskClient> {
    const url = this.baseUrl + '/api/v2/inspection/types/' + inspectionTypeId;
    return this.http
      .post<CreateInspectionTaskResponse>(url, null)
      .pipe(
        map(
          (response) =>
            new InspectionTaskClient(
              this,
              response.inspectionId,
              this.viewState,
            ),
        ),
      );
  }

  public getFeatureList(taskId: string) {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/features`;
    return this.http.get<GetInspectionTaskFeatureResponse>(url);
  }

  public setEnabledFeatures(
    taskId: string,
    featureMap: { [key: string]: boolean },
  ) {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/features`;
    const request: PatchInspectionTaskFeatureRequest = {
      features: featureMap,
    };
    return this.http.patch(url, request, {
      responseType: 'text',
    }) as Observable<unknown> as Observable<void>;
  }

  public getInspectionMetadata(taskId: string) {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/metadata`;
    return this.http.get<InspectionMetadataOfRunResult>(url);
  }

  public runTask(
    taskId: string,
    request: InspectionRunRequest,
  ): Observable<void> {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/run`;
    return this.http
      .post(url, request, { responseType: 'text' })
      .pipe(map(() => void 0));
  }

  public dryRunTask(
    taskId: string,
    request: InspectionDryRunRequest,
  ): Observable<InspectionDryRunResponse> {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/dryrun`;
    return this.http.post<InspectionDryRunResponse>(url, request);
  }

  public getInspectionData(taskId: string, reporter: DownloadProgressReporter) {
    const url = `${this.baseUrl}/api/v2/inspection/tasks/${taskId}/data`;
    return this.http.head(url, { observe: 'response' }).pipe(
      map((response) => {
        const contentLength = Number(response.headers.get('Content-Length'));
        if (isNaN(contentLength)) {
          throw new Error(`Failed to parse Content-Length header: ${contentLength}`);
        } else {
          // contentLength should be a number >= 0
          return contentLength;
        }
      }),
      switchMap((totalSize) => {
        const chunks = Math.ceil(totalSize / this.MAX_INSPECTION_DATA_DOWNLOAD_CHUNK_SIZE);
        return range(0, chunks).pipe(
          map((index) => {
            const startInBytes = index * this.MAX_INSPECTION_DATA_DOWNLOAD_CHUNK_SIZE;
            const maxSizeInBytes = Math.min(this.MAX_INSPECTION_DATA_DOWNLOAD_CHUNK_SIZE, totalSize - startInBytes);
            const urlWithParams = `${url}?${this.buildRangeQueryParameters(startInBytes, maxSizeInBytes)}`;
            return { index, maxSizeInBytes, urlWithParams };
          }),
          mergeMap(({ index, maxSizeInBytes, urlWithParams }) => {
            return this.http.get(`${urlWithParams}`, { responseType: 'blob' }).pipe(
              map((blob) => {
                reporter(maxSizeInBytes);
                return { index, blob };
              })
            );
          }, this.DATA_DOWNLOAD_CONCURRENCY),
          reduce((acc: Blob[], downloadResult: { index: number, blob: Blob }) => {
            acc[downloadResult.index] = downloadResult.blob;
            return acc;
          }, []),
          map((blobs) => {
            const result = new Blob(blobs);
            if (result.size != totalSize) {
              // The downloaded file is very likely broken if the inspection API works well.
              throw new Error(`Downloaded size: ${result.size} != Content-Length: ${totalSize}`);
            }
            return result;
          })
        )
      })
    )
  }

  private buildRangeQueryParameters(
    startInBytes: number,
    maxSizeInBytes: number,
  ): string {
    return `start=${startInBytes}&maxSize=${maxSizeInBytes}`;
  }

  public getPopup(): Observable<PopupFormRequest | null> {
    const url = this.baseUrl + `/api/v2/popup`;
    return this.http.get<PopupFormRequest | null>(url);
  }

  public validatePopupAnswer(
    answer: PopupAnswerResponse,
  ): Observable<PopupAnswerValidationResult> {
    const url = this.baseUrl + `/api/v2/popup/validate`;
    return this.http.post<PopupAnswerValidationResult>(url, answer);
  }
  public answerPopup(answer: PopupAnswerResponse): Observable<void> {
    const url = this.baseUrl + `/api/v2/popup/answer`;
    return this.http.post(url, answer).pipe(map(() => {}));
  }

  public cancelInspection(taskId: string) {
    const url = this.baseUrl + `/api/v2/inspection/tasks/${taskId}/cancel`;
    return this.http
      .post(url, null, { responseType: 'text' })
      .pipe(map(() => {}));
  }

  public uploadFile(
    token: UploadToken,
    file: File,
  ): Observable<HttpEvent<unknown>> {
    const url = this.baseUrl + `/api/v2/upload`;
    const formData = new FormData();
    formData.append('upload-token-id', token.id);
    formData.append('file', file, file.name);
    return this.http.post(url, formData, {
      reportProgress: true,
      observe: 'events',
    });
  }
}

export class InspectionTaskClient {
  private static DRYRUN_DEBOUNCE_DURATION = 100;

  public features = new ReplaySubject<InspectionFeature[]>(1);

  private dryRunParameter = new Subject<InspectionDryRunRequest>();

  private nonFormParameters = concat(this.viewState.timezoneShift).pipe(
    map((tzShift) => ({
      timezoneShift: tzShift,
    })),
    shareReplay(1),
  );

  public dryRunResult = this.dryRunParameter.pipe(
    debounceTime(InspectionTaskClient.DRYRUN_DEBOUNCE_DURATION),
    switchMap((param) => this.dryrunDirect(param)),
    shareReplay(1),
  );

  constructor(
    private readonly api: BackendAPI,
    public readonly taskId: string,
    private readonly viewState: ViewStateService,
  ) {
    this.downloadFeatureList();
  }

  public downloadFeatureList() {
    return this.api
      .getFeatureList(this.taskId)
      .pipe(map((r) => r.features))
      .subscribe((features) => this.features.next(features));
  }

  public setFeatures(featuresMap: { [key: string]: boolean }) {
    return this.api
      .setEnabledFeatures(this.taskId, featuresMap)
      .subscribe(() => {
        this.downloadFeatureList();
      });
  }

  public run(request: InspectionRunRequest) {
    return this.getRunParameter(request).pipe(
      switchMap((request) => {
        return this.api.runTask(this.taskId, request);
      }),
      map(() => {}),
    );
  }

  public dryrun(request: InspectionDryRunRequest) {
    this.dryRunParameter.next(request);
  }

  /**
   * dryrunDirect calls the dryrun API directly without debouncing.
   * This method is public for testing purpose. Use dryrun method instead.
   */
  public dryrunDirect(request: InspectionDryRunRequest) {
    return this.getRunParameter(request).pipe(
      switchMap((request) => this.api.dryRunTask(this.taskId, request)),
    );
  }

  private getRunParameter(
    request: InspectionRunRequest | InspectionDryRunRequest,
  ): Observable<{ [key: string]: unknown }> {
    return of(request).pipe(
      withLatestFrom(this.nonFormParameters),
      map(([request, nonForm]) => ({
        ...request,
        ...nonForm,
      })),
    );
  }
}

/**
 * Utility functions using BackendAPI interface
 */
export class BackendAPIUtil {
  /**
   * Save the inspection data as a file
   */
  public static downloadInspectionDataAsFile(
    api: BackendAPI,
    taskId: string,
    progress: ProgressDialogStatusUpdator,
  ) {
    progress.show();
    return api.getInspectionMetadata(taskId).pipe(
      switchMap((metadata) =>
        forkJoin([
          of(metadata),
          api.getInspectionData(taskId, (done) => {
            const fileSize = metadata.header.fileSize ?? 0;
            progress.updateProgress({
              message: `Downloading inspection data (${ProgressUtil.formatPogressMessageByBytes(done, fileSize)})`,
              percent: (done / fileSize) * 100,
              mode: 'determinate',
            });
          }),
        ]),
      ),
      map(([metadata, blob]) => {
        if (blob === null) return;
        const link = document.createElement('a');
        link.download = metadata.header.suggestedFilename;
        link.href = window.URL.createObjectURL(blob);
        link.style.display = 'none';
        document.body.appendChild(link);
        link.click();
        link.remove();
        progress.dismiss();
        return metadata.header.suggestedFilename;
      }),
    );
  }
}
