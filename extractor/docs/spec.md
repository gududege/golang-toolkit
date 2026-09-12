# Text Extractor CLI Specification

## Overview

工具定位：

> 基于 Regex Named Group 的文本提取 CLI 工具。支持多种编码自动转换，将非结构化文本转换为结构化数据或分割文件。
> 处理流程：

```text
Input File (Read Entire File)
        |
        v
Decode to UTF-8 (Input Encoding)
        |
        v
Regex Matcher (Internal UTF-8)
        |
        v
Named Groups Records
        |
        +-----------------+
        |                 |
        v                 v
 Split Text          Structured Export
 (Output Encoding)   (Always UTF-8)
```

## Environment

### Go Version

最低版本：**Go 1.25**

### 依赖列表

**核心框架**:

- `github.com/spf13/pflag`
- `github.com/spf13/viper`
- `github.com/go-viper/mapstructure/v2`
  **功能库**:
- `modernc.org/sqlite` (Pure Go SQLite)
- `golang.org/x/text` (Encoding Conversion)
- `github.com/jedib0t/go-pretty/v6` (Table Output)
  **标准库**:
- `regexp`, `encoding/json`, `encoding/csv`
- `database/sql`, `path/filepath`, `time`, `os`, `bufio`, `io`

## CLI 参数规则

### 命名规范

统一格式：`--<section>-<option>`

### Short Option (Required Only)

仅 Required 参数提供短参数，Optional 参数不提供短参数。

| Long                | Short | 说明       |
| ------------------- | ----- | ---------- |
| `--config`          | `-c`  | 参数文件   |
| `--input-file`      | `-i`  | 输入文件   |
| `--input-directory` | `-d`  | 输入目录   |
| `--regex-pattern`   | `-r`  | 正则表达式 |

指定config参数时，不再检验其它required参数
--input-file和--input-directory必须指定一个，且**二者互斥，不能同时指定**；同时指定时报错退出（Exit Code 2）
没有split，没有任何export时，输出regex正则匹配结果统计到stdout（不输出结果）

### Positional Argument (Drag & Drop)

支持将配置文件直接拖拽到可执行文件上运行：

- 允许**至多一个**位置参数，作为 config 文件路径，等价于 `--config`
- 位置参数与 `--config` 同时指定报参数错误（Exit Code 2）
- 多于一个位置参数报参数错误（Exit Code 2）

```text
extractor config.yaml        # 等价于 extractor --config config.yaml
```

### 简写规则

- 移除旧版中省略 section 的简写（如 `--file`）。
- 用户要么使用 Short Option（如 `-i`），要么使用完整 Long Option（如 `--input-file`）。

## Config File

支持格式：`toml`, `yaml`（JSON 配置格式已移除，`.json` 配置文件报参数错误 Exit Code 2）。
优先级：

```text
CLI > Config File > Default
```

## Configuration Object

```go
type Config struct {
    ConfigPath string         `mapstructure:"-"`
    Input  InputConfig  `mapstructure:"input"`
    Regex  RegexConfig  `mapstructure:"regex"`
    Split  SplitConfig  `mapstructure:"split"`
    Export ExportConfig `mapstructure:"export"`
    DryRun bool         `mapstructure:"dry-run"`
    Regexes []RegexTaskConfig `mapstructure:"regexes"`
}
```

## Regexes (Multi-Regex Mode)

配置文件中定义 `regexes` 数组即启用多正则并行模式。输入文件只读取/解码一次，被所有任务共享；每个任务并行执行正则匹配，输出（split / export / 统计）按配置顺序串行执行。

### Struct

```go
type RegexTaskConfig struct {
    Name    string        `mapstructure:"name"`    // 可选，用于错误与警告定位
    Pattern string        `mapstructure:"pattern"` // 必填
    Flags   string        `mapstructure:"flags"`
    Split   *SplitConfig  `mapstructure:"split"`   // 可选，任务独立 split
    Export  *ExportConfig `mapstructure:"export"`  // 可选，任务独立 export
}
```

### Rules

- **互斥**：`regexes` 与顶层 `regex` / `split` / `export` 互斥，同时配置报参数错误（Exit Code 2）。CLI 单正则参数不受影响。
- **必填**：每个任务必须提供 `pattern`，否则 Exit Code 2。
- **输出冲突规则**：
  - 不同导出类别（json / csv / sqlite）不得指向同一个文件，冲突报参数错误（Exit Code 2）。路径比较先做 `filepath.Clean` 规范化，并在大小写不敏感平台（Windows）折叠大小写，`./out.json` 与 `out.json`、`Out.JSON` 与 `out.json` 视为同一文件。
  - 同一类别不同任务允许指向同一个文件：匹配并行执行，输出按配置顺序串行写入，由各任务自身的 `export.append` 决定覆盖还是追加；共享时向 stderr 打印提示警告（非致命）。
- **默认值**：任务未指定的导出选项（`sort`、`pretty-level`、`delimiter`）沿用与单正则模式相同的默认值。
- **执行模型（两阶段）**：先串行编译所有正则（任一编译失败立即 Exit Code 4，错误信息带任务名）；阶段 1 各任务以 goroutine 并行执行正则匹配（CPU 密集部分共享输入文件）；阶段 2 按配置顺序串行执行各任务的 split / export / 统计输出，保证同文件多次写入无竞态、结果确定，dry-run 与统计输出顺序可预测。
- **无匹配**：单个任务无匹配仅打印 `warning: <task>: No matches found` 并继续；**全部**任务均无匹配才报 "No matches found"（Exit Code 3）。其余执行错误取第一个（按任务顺序），Exit Code 1。

## Input

### Struct

```go
type InputConfig struct {
    File      string `mapstructure:"file"`
    Directory string `mapstructure:"directory"`
    Pattern   string `mapstructure:"pattern"`
    Encoding  string `mapstructure:"encoding"`
}
```

### CLI

| 参数                | Short | 说明                             |
| ------------------- | ----- | -------------------------------- |
| `--input-file`      | `-i`  | 输入文件                         |
| `--input-directory` | `-d`  | 输入目录                         |
| `--input-pattern`   |       | 目录文件匹配（glob 语法，默认匹配所有文件） |
| `--input-encoding`  |       | 文件编码                         |

`--input-pattern` 使用标准库 `path/filepath` 的 glob 语法（如 `*.txt`、`log-*.csv`），非正则表达式。

### Encoding Support

| Value     | 说明                    |
| --------- | ----------------------- |
| `utf8`    | UTF-8 (默认, 无 BOM)    |
| `utf8bom` | UTF-8 with BOM          |
| `gbk`     | GBK (简体中文)          |

不支持 UTF-16 编码。

### Behavior

**一次性读取**：
由于支持正则 `multiline` 模式，工具必须一次性将整个文件读入内存，不支持流式逐行处理。

**多文件处理模型（目录模式）**：
当使用 `--input-directory` 时，目录下匹配 `--input-pattern` 的每个文件**独立处理**：各自独立读取、独立解码、独立执行正则匹配，产生的 Named Groups 记录再汇总用于后续 Split / Export。Split 中"仅指定后缀名"的文件名生成规则（如 `source_filename_1.txt`）即基于此逐文件独立处理的模型，`source_filename` 取自触发该条记录的源文件名。

---

## Regex

### Struct

```go
type RegexConfig struct {
    Pattern string `mapstructure:"pattern"`
    Flags   string `mapstructure:"flags"`
}
```

### CLI

| 参数              | Short | 说明       |
| ----------------- | ----- | ---------- |
| `--regex-pattern` | `-r`  | 正则表达式 |
| `--regex-flags`   |       | 正则标志   |

### Flags

支持：`m` (multiline), `s` (dotall), `i` (ignore case)。

### Unmatched Optional Group

若某条记录中因分支未命中导致某个 Named Group 未捕获到值（可选分组），该字段在输出中**保留字段，值为空**（CSV/JSON/SQLite 均输出空字符串，不省略该字段），而不是跳过该字段。

### Error Handling

**正则编译错误**：
若 `--regex-pattern` 语法错误导致编译失败（Go `regexp2`，基于 .NET 正则引擎，支持反向引用、环视断言 `(?=...)`/`(?!...)`/`(?<=...)`/`(?<!...)` 等特性），工具应报错并退出（Exit Code 4），提示具体编译错误信息。

**无匹配报错**：
若正则表达式在输入数据中未匹配到任何结果，工具应报错并退出（Exit Code 3），提示 "No matches found"。

---

## Split Export

将指定 Named Group 内容输出为独立文本文件。

### Struct

```go
type SplitConfig struct {
    Group     string `mapstructure:"group"`
    OutputDir string `mapstructure:"output-dir"`
    File      string `mapstructure:"file"`
    Encoding  string `mapstructure:"encoding"`
}
```

### Output Dir

`output-dir` 与 `file` 一样支持 `{group_name}` 简单占位符替换（非 go template），可将不同记录写入不同子目录，例如：

```text
out/{level} → out/ERR/...、out/INFO/...
```

- 渲染后的目录路径逐段清洗非法文件名字符（保留分隔符与 Windows 盘符），渲染结果为空时回退 `.`。
- 子目录按记录自动递归创建；`dry-run` 模式下仅打印目标路径，不创建目录。
- 重名去重（`_1`、`_2` 后缀）跨任务生效：多正则模式下所有任务共享已占用路径登记，不同任务产生的同名 split 文件不会互相覆盖。

### CLI

| 参数                 | 说明                   |
| -------------------- | ---------------------- |
| `--split-group`      | **必填**，Group名称    |
| `--split-output-dir` | 输出目录               |
| `--split-file`       | 文件名模板（包括扩展） |
| `--split-encoding`   | 输出编码               |

### Enable Logic

- **触发条件**：CLI 中提供了 `--split-group` 即视为启用。

### File

文件名可以使用正则中的 group name 通过**简单变量替换**（非 go template，仅做字符串占位符替换）动态生成，比如

```go
{group_name}.txt
{group_name1}_{group_name2}.csv
```

若 `--split-file` 未指定，默认以匹配到的 group 内容为文件名，追加 `.txt` 后缀。

重名文件自动在后面添加\_index，比如

```go
name.txt -> name.txt
         -> name_1.txt
         -> name_2.txt
         -> name_3.txt
```

如果只指定了后缀名，就用原始文件加index来生成文件名，比如

```go
.txt -> source_filename_1.txt
     -> source_filename_2.txt
     -> source_filename_3.txt
```

### Encoding Support

输出编码支持：`utf8`, `utf8bom`, `gbk`。不支持 UTF-16 编码。

### File System Behavior

- **自动创建目录**：`output-dir` 不存在时自动递归创建。
- **强制覆盖**：若目标文件存在，直接覆盖，无需用户确认。

---

## Export

### Struct

```go
type ExportConfig struct {
    Append bool              `mapstructure:"append"`
    Sort   string            `mapstructure:"sort"`
    JSON   JSONExportConfig  `mapstructure:"json"`
    CSV    CSVExportConfig   `mapstructure:"csv"`
    SQLite SQLiteExportConfig `mapstructure:"sqlite"`
}
```

### Common Behavior

- **编码强制**：所有 Export 输出（JSON/CSV/SQLite）强制使用 **UTF-8**。
- **目录创建**：自动创建输出文件所在目录。
- **文件覆盖**：非 Append 模式下，文件存在则直接覆盖。

### Sort Property Order

通过 `--export-sort` 指定输出属性排序。支持 `asc`（升序）、`desc`（降序）、`none`（默认，保持正则 group 原始顺序）。

CLI: `--export-sort`

### Append Logic

通过 `--export-append` 启用。

当目标文件不存在时，append 模式自动降级为创建模式（等同于首次导出）。

**CSV Append** **Sqlite Append**:

1. 读取现有文件/数据库的 Header。
2. 与当前数据的 Header 进行比对。
3. 若不一致，报错退出。
4. 若一致，跳过 Header 写入，仅追加数据行。

**JSON Append**:

1. 读取现有 JSON Array。
2. 检查第一条记录的字段结构。
3. 若结构兼容，将新记录追加到 Array 末尾并重新写入。

## JSON Export

### Struct

```go
type JSONExportConfig struct {
    Enabled bool `mapstructure:"enabled"`
    File string `mapstructure:"file"`
    PrettyLevel int `mapstructure:"pretty-level"`
}
```

### CLI

```
--export-json-file
--export-json-pretty-level
```

省略enabled，有--export-json-file表示要导出json

### Pretty Level

| 值  | 格式     |
| --- | -------- |
| 0   | compact  |
| 1   | 一级缩进 |
| 2   | 完整缩进 |

默认值1

## CSV Export

### Struct

```go
type CSVExportConfig struct {
    Enabled bool `mapstructure:"enabled"`
    File string `mapstructure:"file"`
    Delimiter string `mapstructure:"delimiter"`
}
```

### CLI

```
--export-csv-file
--export-csv-delimiter
```

CLI省略enabled，有--export-csvfile表示要导出csv

--export-csv-delimiter可选',' '|' ';' '\t'四种，默认值为','

### Behavior

- 新建文件时写入 Header。
- Append 时不重复写入 Header。

## SQLite Export

### Struct

```go
type SQLiteExportConfig struct {
    Enabled bool `mapstructure:"enabled"`
    File string `mapstructure:"file"`
    Table string `mapstructure:"table"`
    OverrideType string `mapstructure:"override-type"`
}
```

### CLI

```
--export-sqlite-file
--export-sqlite-table
--export-sqlite-override-type
```

`--export-sqlite-table` 默认值为 `data`。

### Override Type

格式：

```text
column:type,column:type
```

Example:

```text
time:datetime,count:int64,name:string
```

Datetime 类型支持指定 Go time layout，layout 必须用单引号或双引号包裹：

```text
time:datetime:"2006-01-02",stamp:datetime:'2006-01-02 15:04:05'
```

若 layout 无效（无法满足 Go `time.Parse` 要求），报错退出。

解析：

```go
map[string]overrideEntry{
"time":{sqlType:"DATETIME", layout:"2006-01-02"},
"count":{sqlType:"INTEGER"},
"name":{sqlType:"TEXT"},
}
```

### 支持类型

```go
string
int
int32
int64
float
double
bool (also accepts boolean)
date
datetime
```

### 类型推断

优先级：

```text
Override Type
>
Auto Detect (int / float / bool)
>
TEXT
```

**Datetime 不支持自动推断**，必须通过 `override-type` 显式指定，格式为 `column:datetime:"layout"`。

### Auto Detect

Example:
输入：

```text
id=100
temperature=23.5
enabled=true
```

生成：

```sql
CREATE TABLE logs
(
id INTEGER,
temperature REAL,
enabled BOOLEAN
)
```

Datetime 字段示例（通过 override 指定）：

```
--export-sqlite-override-type "time:datetime:\"2006-01-02 15:04:05\""
```

输入含 `time=2026-07-31 12:00:00` 时将创建 `time DATETIME` 列。

**类型不一致处理**：
类型推断基于该 Group 在**全部记录**中的取值。若同一 Group 在不同记录间推断出的类型不一致（如某条 `count=abc` 而其余为数字），该 Group 整体**退化为 `TEXT`** 类型，不报错。

## Dry Run

### CLI

```
--dry-run
```

### Behavior

- 不创建任何文件。
- 输出到 stdout。

### Output Format

- **Split**: 标准文本块。
- **JSON**: 标准 JSON 字符串。
- **CSV**: **原始 CSV 文本**（不使用表格格式）。

```csv
  time,level,message
  10:00,ERROR,failed
```

- **SQLite**: 表格形式显示 Schema 和数据预览。

---

## Exit Codes

| Code | 状态             |
| ---- | ---------------- |
| 0    | 成功             |
| 1    | 执行错误         |
| 2    | 参数解析错误     |
| 3    | 正则无匹配       |
| 4    | 正则编译错误     |

多正则（`regexes`）模式下：任一任务编译错误 → 4；全部任务无匹配 → 3；单个任务无匹配仅警告（不影响退出码）；执行错误取第一个任务错误 → 1。

## Package Selection (Go)

### CLI Framework

#### Pflag

用途：

POSIX 风格参数：

```go
-i
-r
--input-file
```

Package:

```go
github.com/spf13/pflag
```

### Config

#### Viper

支持：

- TOML
- YAML
- CLI override

Package:

```
github.com/spf13/viper
```

### Map Decode

Package:

```
github.com/go-viper/mapstructure/v2
```

### Regex

Go Package:

```
github.com/dlclark/regexp2
```

支持：

- Named Group
- UTF8
- 前向断言 `(?=...)` / `(?!...)`
- 后向断言 `(?<=...)` / `(?<!...)`
- 反向引用 `\1`

### JSON

Go Standard:

```
encoding/json
```

### CSV

Go Standard:

```
encoding/csv
```

### SQLite

推荐：

```
modernc.org/sqlite
```

database:

```
database/sql
```

### Table Output

Dry-run 表格：

推荐：

```
github.com/jedib0t/go-pretty/v6/table
```

用途：

- SQLite preview
- terminal table

### 文件匹配

目录扫描：

Go Standard:

```
path/filepath
```

### 时间解析

SQLite datetime 推断：

Go Standard:

```
time
```

### 最终依赖列表

```
github.com/spf13/pflag
github.com/spf13/viper
github.com/go-viper/mapstructure/v2
github.com/dlclark/regexp2
modernc.org/sqlite
golang.org/x/text
github.com/jedib0t/go-pretty/v6
```

标准库：

```
encoding/json
encoding/csv
database/sql
path/filepath
time
os
bufio
io
slices
```
