/*
 * Copyright 2019 National Library of Norway.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *       http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package constants

// Http headers
const (
	HeaderAcceptEncoding   = "Accept-Encoding"
	HeaderCrawlExecutionId = "veidemann_eid"
	HeaderJobExecutionId   = "veidemann_jeid"
	HeaderCollectionId     = "veidemann_cid"
	HeaderProxyErrorCode   = "X-Recoderproxy-Err-Code"
	HeaderProxyError       = "X-Recoderproxy-Err"
)

// Record constants
const (
	RecordRequest             = "request"
	RecordResponse            = "response"
	RecordContentTypeRequest  = "application/http; msgtype=request"
	RecordContentTypeResponse = "application/http; msgtype=response"
)
