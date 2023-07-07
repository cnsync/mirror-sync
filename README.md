## 借助 skopeo 工具将无法访问的镜像同步至国内, 解决国内无法访问问题

- 支持 docker.io
- 支持 aliyuncs.com

## 待增加

- huaweicloud.com


## 命令参数
```
NAME:
   mirror-sync - 同步Docker镜像

USAGE:
   mirror-sync [global options] command [command options] [arguments...]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --mirror value       要同步的镜像的名称存储地址 (default: "https://raw.githubusercontent.com/cnsync/mirror-sync/main/mirrors-docker.txt")
   --concurrency value  并发请求数 (default: 5)
   --hub value          需要同步的目的仓库 (default: "docker.io/cnxyz")
   --help, -h           show help
```