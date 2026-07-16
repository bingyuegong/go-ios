# go-ios

跨平台 iOS 自动化命令行工具，支持 macOS / Linux / Windows。

## 安装

```bash
go install github.com/bingyuegong/go-ios@latest
```

或从 [Releases](https://github.com/bingyuegong/go-ios/releases) 下载预编译二进制。

## 快速开始

```bash
# 查看版本
go-ios version

# 列出已连接设备
go-ios list

# 查看帮助
go-ios --help
go-ios help <命令>   # 查看具体命令帮助，例如：go-ios help runwda
```

## 全局参数

所有命令均支持以下全局参数：

| 参数 | 说明 |
|------|------|
| `-u <udid>` | 指定设备 UDID（多设备时必填，也可设置环境变量 `GO_IOS_UDID`） |
| `-v` / `--verbose` | 开启调试日志 |
| `-t` / `--trace` | 开启追踪日志（打印所有消息） |
| `--nojson` | 禁用 JSON 输出，改为人类可读格式 |
| `--pretty` | JSON 输出格式化缩进 |
| `--proxyurl <url>` | 设置出站 HTTP 代理，例如 `http://user:pass@ip:port` |

## 设备管理

```bash
# 列出已连接设备（表格格式）
go-ios list

# 列出设备（JSON 格式）
go-ios list -J

# 配对设备
go-ios pair

# 配对受监管设备（需要 p12 文件）
go-ios pair -p12file supervisor.p12 -password 123456

# 重启设备
go-ios reboot

# 关机
go-ios shutdown

# 擦除设备（危险操作）
go-ios erase --force

# 激活设备
go-ios activate

# 查看设备名称
go-ios devicename

# 查看设备信息
go-ios info
go-ios info display    # 显示屏信息
go-ios info lockdown   # lockdown 信息

# 查看设备日期
go-ios date

# 查看磁盘空间
go-ios diskspace

# 查看电池信息
go-ios batterycheck
go-ios batteryregistry

# 开发者模式
go-ios devmode enable   # 开启
go-ios devmode get      # 查询状态
go-ios devmode reveal   # 在设置中显示开关
```

## 应用管理

```bash
# 列出已安装应用（简洁格式：包名 应用名 版本）
go-ios apps

# 列出所有应用（含系统应用）
go-ios apps --all

# 列出系统应用
go-ios apps --system

# 列出支持文件共享的应用
go-ios apps --filesharing

# JSON 格式输出（包含包名、应用名、版本）
go-ios apps -J

# 安装应用
go-ios install /path/to/app.ipa
go-ios install -p /path/to/app.ipa
go-ios install -u <udid> /path/to/app.ipa   # 指定设备

# 卸载应用
go-ios uninstall com.example.app

# 启动应用
go-ios launch com.example.app

# 启动应用并等待（保持连接以查看日志）
go-ios launch com.example.app --wait

# 启动应用并传参数/环境变量
go-ios launch com.example.app -arg arg1 -arg arg2 -env KEY=value

# 杀死应用
go-ios kill com.example.app
go-ios kill -pid 1234
go-ios kill -process MyApp

# 查看运行中的进程
go-ios ps
go-ios ps --apps   # 仅显示应用进程
```

## 运行测试 / WDA

```bash
# 启动 WebDriverAgent（使用默认 bundle ID）
go-ios runwda

# 启动自定义 WDA
go-ios runwda \
  -bundleid com.yourcompany.WebDriverAgentRunner.xctrunner \
  -testrunnerbundleid com.yourcompany.WebDriverAgentRunner.xctrunner \
  -xctestconfig WebDriverAgentRunner.xctest

# 启动 WDA 并输出日志到文件
go-ios runwda -log-output /tmp/wda.log

# 启动 WDA 并输出日志到标准输出
go-ios runwda -log-output -

# 运行 XCUITest
go-ios runtest -bundle-id com.example.app.xctrunner

# 运行 XCUITest（完整参数）
go-ios runtest \
  -bundle-id com.example.UITests.xctrunner \
  -test-runner-bundle-id com.example.UITests.xctrunner \
  -xctest-config UITests.xctest \
  -log-output -

# 运行指定测试用例
go-ios runtest -bundle-id com.example.app.xctrunner \
  -test-to-run TestClass/testMethod

# 运行 .xctestrun 文件
go-ios runxctest -xctestrun-file-path /path/to/test.xctestrun
```

## UI 自动化（WDA / DeviceKit）

```bash
# 安装 WDA（需要签名文件）
go-ios ui install wda -p12file signing.p12 -profile app.mobileprovision

# 安装 DeviceKit
go-ios ui install devicekit -p12file signing.p12 -profile app.mobileprovision

# 使用本地 WDA 包安装
go-ios ui install wda -p /path/to/wda.ipa -p12file signing.p12 -profile app.mobileprovision

# 启动 WDA 并转发端口（阻塞运行）
go-ios ui run wda

# 启动 DeviceKit
go-ios ui run devicekit

# 截图
go-ios ui screenshot
go-ios ui screenshot -output screen.png

# 点击坐标
go-ios ui tap -x 100 -y 200

# 滑动
go-ios ui swipe -from-x 100 -from-y 500 -to-x 100 -to-y 100

# 长按
go-ios ui longpress -x 100 -y 200 -duration 2.0

# 输入文字
go-ios ui type -text "Hello World"

# 按键
go-ios ui button home

# 获取 UI 层级
go-ios ui source

# 获取屏幕尺寸
go-ios ui size

# 启动/终止应用
go-ios ui app launch com.example.app
go-ios ui app terminate com.example.app

# 视频流
go-ios ui stream mjpeg
go-ios ui stream h264
```

## iOS 17+ Tunnel（隧道）

iOS 17+ 设备需要先建立隧道才能使用部分功能：

```bash
# 启动隧道（需要 sudo / 管理员权限）
sudo go-ios tunnel start

# 列出运行中的隧道
go-ios tunnel ls

# 停止隧道代理
go-ios tunnel stopagent
```

## 文件操作

```bash
# 列出应用沙盒文件
go-ios file ls -app com.example.app

# 列出应用组文件
go-ios file ls -app-group group.com.example

# 下载文件（iOS 17+，需要隧道）
go-ios file pull -app com.example.app -remote /Documents/data.db -local ./data.db

# 上传文件（iOS 17+，需要隧道）
go-ios file push -app com.example.app -local ./data.db -remote /Documents/data.db

# fsync 文件同步（旧版 iOS）
go-ios fsync -app com.example.app pull -srcPath /Documents/data.db -dstPath ./data.db
go-ios fsync -app com.example.app push -srcPath ./data.db -dstPath /Documents/data.db
go-ios fsync -app com.example.app tree -p /Documents
```

## 截图与录屏

```bash
# 截图（保存到当前目录）
go-ios screenshot

# 截图并指定输出路径
go-ios screenshot -output screen.png

# 启动 MJPEG 流（默认端口 3333）
go-ios screenshot --stream

# 指定端口
go-ios screenshot --stream -port 8080
```

## 位置模拟

```bash
# 设置模拟位置（经纬度）
go-ios setlocation -lat 39.9042 -lon 116.4074

# 从 GPX 文件设置位置
go-ios setlocationgpx -gpxfilepath /path/to/location.gpx

# 重置位置
go-ios resetlocation
```

## 日志

```bash
# 实时查看设备日志
go-ios syslog

# 解析日志字段
go-ios syslog --parse

# os_trace 日志流
go-ios ostrace
go-ios ostrace -process SpringBoard
go-ios ostrace -pid 1234 -level debug
```

## 崩溃日志

```bash
# 列出崩溃日志
go-ios crash ls

# 按模式过滤
go-ios crash ls "*MyApp*"

# 下载崩溃日志
go-ios crash cp "*" ./crashes

# 删除崩溃日志
go-ios crash rm "." "*"
```

## 签名

```bash
# 对 IPA 重签名
go-ios sign app \
  -p /path/to/app.ipa \
  -p12file signing.p12 \
  -profile app.mobileprovision \
  -p12password 123456

# 重签名并直接安装
go-ios sign app \
  -p /path/to/app.ipa \
  -p12file signing.p12 \
  -profile app.mobileprovision \
  --install

# 通过 App Store Connect 创建签名资产
go-ios sign provision appstoreconnect \
  -bundleid com.example.app \
  -asc-key-id KEYID \
  -asc-issuer-id ISSUERID \
  -asc-private-key /path/to/AuthKey.p8 \
  -profile-output app.mobileprovision
```

## 其他常用命令

```bash
# 查看语言/地区设置
go-ios lang

# 设置语言
go-ios lang -setlang zh-Hans -setlocale zh_CN

# 查看 lockdown 值
go-ios lockdown get
go-ios lockdown get DeviceName
go-ios lockdown get -domain com.apple.disk_usage

# 查询 mobilegestalt
go-ios mobilegestalt UniqueDeviceID

# 端口转发
go-ios forward 8100 8100
go-ios forward -port 8100:8100 -port 9191:9191

# 网络抓包
go-ios pcap > capture.pcap
go-ios pcap -process Safari > safari.pcap

# 壁纸
go-ios get-wallpaper -output wallpaper.png
go-ios set-wallpaper /path/to/image.jpg

# 图标布局
go-ios get-icon-layout -output layout.json
go-ios set-icon-layout layout.json

# 辅助功能
go-ios assistivetouch enable
go-ios assistivetouch get

# 开发者镜像
go-ios image auto          # 自动下载并挂载
go-ios image list          # 列出已挂载镜像
go-ios image mount -p /path/to/image
go-ios image unmount
```

## 多设备使用

连接多台设备时，使用 `-u` 指定目标设备：

```bash
# 查看所有设备
go-ios list

# 对指定设备执行命令
go-ios -u 00008110-000E2C8C3AF1401E apps
go-ios -u 00008110-000E2C8C3AF1401E install /path/to/app.ipa
go-ios -u 00008110-000E2C8C3AF1401E runwda

# 也可以通过环境变量指定
export GO_IOS_UDID=00008110-000E2C8C3AF1401E
go-ios apps
```

## 参数说明

- **布尔开关**（无值参数）使用 `--` 前缀，例如：`--nojson`、`--verbose`、`--stream`、`--force`、`--install`
- **带值参数**使用 `-` 前缀加空格，例如：`-u <udid>`、`-bundleid <id>`、`-output <file>`
- 可重复参数使用多次，例如：`-arg arg1 -arg arg2`、`-env KEY=val`