# Changelog

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
