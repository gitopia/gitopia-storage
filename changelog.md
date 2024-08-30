# Changelog

## [v3.0.0] - 2024-08-20
- upgrade gitopia, gitopia-go and cosmos-sdk dependencies
- improve the dockerfile for localnet testing
- add build flags for reducing binary size
- increase the gas adjustment to 1.8 to prevent out of gas error after the sdk upgrade
- remove the bash dependency from the git cli calls

## [v2.2.0] - 2023-09-22
- Configure grpc host during build

## [v2.1.0] - 2023-07-27

- fix pr diff api to return git triple dot diff
- add line filter to content API

## [v2.0.1] - 2023-05-18

- fix pre receive hook for empty branch
- close reponse writer for POST rpc calls

## [v2.0.0] - 2023-05-17

- Added support for git lfs
- Use git cli instead of go-git for pack-protocol
- Changed http auth from Bearer to Basic
- Refactored routes
- Upgrade gitopia go lib to use fee grants for tx
- Upgrade go version to 1.19

## [v1.8.0] - 2023-02-22

- Upgrade gitopia version to v1.3.0

## [v1.7.0] - 2023-02-09

- Fix event resubscription on reconnection
- Move websocket logic to gitopia-go
- Add websocket debug logs

## [v1.6.0] - 2023-01-17

- Fix websocket connection error
- Refactor: use gitopia-go client library

## [v1.5.0] - 2022-12-23

- use cosmos-sdk client instead of ignite client

## [v1.4.0] - 2022-11-08

- new /raw api to get raw files

## [v1.3.0] - 2022-11-07

- Bump gitopia version to v1.2.0

## [v1.2.0] - 2022-11-02

- Bump gitopia version to v1.1.2

## [v1.1.1] - 2022-11-01

- Set gas calculation to auto

## [v1.1.0] - 2022-10-27

- Bump gitopia version to v1.1.0

## [v1.0.0] - 2022-10-20

- Bump gitopia version to v1.0.0
- Add authentication in push api
- Don't cache git objects larger than 1Mb in go-git
- Fix certain diffs
- Pass codec during grpc client initialization

## [v0.6.1] - 2022-07-28

### Fixes

- Wrap path in double quotes

## [v0.6.0] - 2022-04-04

### Features

- New service which processes fork repo and merge pr events

### Fixes

- Fix next_key in commits api response

## [v0.5.0] - 2022-02-15

### Features

- Add option to include lastCommit in `/content` API
- API to get repository commits

## [v0.4.0] - 2022-02-03

### Fixes

- Fixed the attachments API
- Use the patched go-git library
- Optimize the docker build

### Features

- API to fork a repository
- tree and blob API
- API to get diff of pull requests
- Pull request APIs: commits, check and merge
