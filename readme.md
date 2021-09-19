# gitopia services

gitopia services for [gitopia](https://gitopia.org/)

## Build

Building gitopia services requires [Go 1.16+](https://golang.org/dl/).

```
make build
```

## Usage

Make necessary changes in `config.toml` for production and also set the following environment variable. Create `git_dir` and `attachments_dir` and verify the permissions.

```sh
export ENV=PRODUCTION
```

To start the server, execute the following command

```sh
./build/main
```

The server will be listening at port `5000`

## Available APIs

- `GET` /objects/<repository_id>/<object_hash> : get loose git object
- `POST` /save : save newly pushed objects to Arweave
- `POST` /upload : upload release/issue/pull_request/comment attachments
- `GET` /releases/<address>/<repositoryName>/<tagName>/<fileName> : get attachment
- `GET` /info/refs
- `POST` /git-upload-pack
- `POST` /git-receive-pack
