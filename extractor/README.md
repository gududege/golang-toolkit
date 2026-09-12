# Extractor

基于正则 Named Group 的文本提取 CLI 工具。将非结构化文本（日志、配置文件、数据导出等）通过正则匹配转换为结构化数据（JSON / CSV / SQLite）或分割为独立文本文件。

## 处理流程

```
Input File (一次性读入内存)
        │
        v
解码为 UTF-8（支持 utf8 / utf8bom / gbk）
        │
        v
正则匹配（regexp2，支持环视/反向引用）
        │
        v
Named Groups Records
        │
        +─────────────+──────────────+
        │                            │
        v                            v
   Split 文本输出               结构化导出
  (可指定输出编码)              (统一 UTF-8)
                            JSON / CSV / SQLite
```

## 安装

```bash
go install github.com/gududege/golang-toolkit/extractor@latest
```

要求 Go 1.25+。

## 快速开始

### 最简用法：统计匹配

```bash
extractor -i access.log -r '(?P<time>\S+) (?P<method>\w+) (?P<path>\S+)'
```

输出匹配数和 group 名称，不写文件。

### 导出 JSON

```bash
extractor -i access.log \
  -r '(?P<time>\S+) (?P<method>\w+) (?P<path>\S+)' \
  --export-json-file out.json
```

### 导出 CSV（自定义分隔符）

```bash
extractor -i data.txt \
  -r '(?P<id>\d+),(?P<name>\w+)' \
  --export-csv-file out.csv \
  --export-csv-delimiter '|'
```

### 分割为独立文件

```bash
extractor -i log.txt \
  -r '(?P<level>\w+): (?P<msg>.*)' \
  --split-group level \
  --split-output-dir ./by-level \
  --split-file '{level}.txt'
```

### 目录批量处理

```bash
extractor -d ./logs --input-pattern '*.log' \
  -r '(?P<timestamp>\d{4}-\d{2}-\d{2}) (?P<event>\w+)' \
  --export-sqlite-file events.db
```

### 使用配置文件

```bash
extractor --config config.yaml
```

也可以把配置文件直接拖拽到可执行文件上运行，等价于 `--config`：

```bash
extractor config.yaml
```

## CLI 参数

### 输入 (Input)

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| `--input-file` | `-i` | 输入文件路径 | — |
| `--input-directory` | `-d` | 输入目录 | — |
| `--input-pattern` | | 目录 glob 匹配 | `*` |
| `--input-encoding` | | 文件编码 | `utf8` |

- `--input-file` 与 `--input-directory` 互斥，至少指定一个（使用 `--config` 时例外）
- 支持编码：`utf8`、`utf8bom`、`gbk`

### 正则 (Regex)

| 参数 | 简写 | 说明 |
|------|------|------|
| `--regex-pattern` | `-r` | 正则表达式（支持 named groups）|
| `--regex-flags` | | 正则标志 |

- Flags: `m` (multiline), `s` (dotall), `i` (ignore case)
- 引擎基于 .NET 正则（regexp2），支持 `(?=...)` `(?<=...)` 等断言
- 可选分组未命中时保留空值字段，不省略

### 分割输出 (Split)

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--split-group` | 触发分割的 group 名称 | — |
| `--split-output-dir` | 输出目录（支持 `{group_name}` 模板） | `.` |
| `--split-file` | 文件名模板 | `{group}.txt` |
| `--split-encoding` | 输出编码 | `utf8` |

- 设置 `--split-group` 即启用分割模式
- 文件名与输出目录均支持 `{group_name}` 占位符，输出目录可按记录值写入不同子目录（如 `out/{level}`）
- 仅指定扩展名（如 `.txt`）时使用 `源文件名_序号.ext`
- 重名自动追加 `_1` `_2` 后缀；多正则模式下该去重跨任务生效，不同任务产生的同名文件不会互相覆盖

### 导出 (Export)

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--export-append` | 追加到已有文件 | `false` |
| `--export-sort` | 属性排序 | `none` |

**JSON**

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--export-json-file` | 输出路径（设置即启用）| — |
| `--export-json-pretty-level` | 缩进: 0=紧凑 1=一级 2=完整 | `1` |

**CSV**

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--export-csv-file` | 输出路径（设置即启用）| — |
| `--export-csv-delimiter` | 分隔符 | `,` |

- 支持分隔符：`,` `|` `;` `\t`

**SQLite**

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--export-sqlite-file` | 输出路径（设置即启用）| — |
| `--export-sqlite-table` | 表名 | `data` |
| `--export-sqlite-override-type` | 列类型覆盖 | — |

- 类型覆盖格式：`col:type,col:type`
- 支持类型：`string` `int` `int32` `int64` `float` `double` `bool` `date` `datetime`
- Datetime 需指定 layout：`time:datetime:"2006-01-02 15:04:05"`
- 优先级：Override > 自动推断 (int/float/bool) > TEXT

### 其他

| 参数 | 简写 | 说明 |
|------|------|------|
| `--config` | `-c` | 配置文件路径 (YAML / TOML) |
| `--dry-run` | | 仅输出到 stdout，不写文件 |

## 配置文件

支持 YAML、TOML（JSON 配置格式已移除）。优先级：**CLI > Config File > Default**。

示例配置参考 `docs/` 目录：
- [`config.yaml`](docs/config.yaml) — 完整注释
- [`config.toml`](docs/config.toml) — TOML 格式

## 多正则并行处理（regexes）

在配置文件中定义 `regexes` 数组即可启用多正则模式：输入文件只读取/解码一次，每个正则任务并行执行匹配，并各自搭配独立的 `split` / `export` 输出。

```yaml
input:
  file: access.log

regexes:
  - name: errors              # 可选，用于错误与警告定位
    pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      json:
        file: errors.json
  - name: all
    pattern: '(?P<level>\w+): (?P<msg>.+)'
    split:
      group: level
      output-dir: 'out/{level}'   # 输出目录同样支持 {group_name} 模板
```

规则：

- `regexes` 与顶层 `regex` / `split` / `export` 互斥，同时配置报参数错误（退出码 2）
- 每个任务必须提供 `pattern`；`name` 可选（缺省用 `regexes[i]` 标识）
- **输出冲突规则**：
  - 不同导出类别（json / csv / sqlite）不得指向同一个文件（含 Windows 大小写变体与 `./` 前缀等规范化等价路径），冲突报参数错误（退出码 2）
  - 同一类别不同任务允许指向同一个文件：正则匹配并行执行，输出按配置顺序串行写入，由各任务自身的 `export.append` 决定覆盖还是追加（如 task1 `append: false` 写入、task2 `append: true` 追加，最终文件 = 两者内容之和）；共享时向 stderr 打印提示警告
- 单个任务无匹配仅打印警告继续执行；全部任务无匹配才报 "No matches found"（退出码 3）
- 任一任务正则编译错误立即退出（退出码 4），错误信息带任务名
- 未在任务内指定的导出选项（`sort`、`pretty-level`、`delimiter` 等）沿用与单正则模式相同的默认值
- 多正则模式仅通过配置文件启用，CLI 单正则参数行为不变

## 退出码

| 码 | 含义 |
|----|------|
| 0 | 成功 |
| 1 | 执行错误 |
| 2 | 参数错误 |
| 3 | 正则无匹配 |
| 4 | 正则编译错误 |

## Dry Run

`--dry-run` 模式下不创建任何文件，输出到 stdout：

- **Split**: 文本块，标注目标路径
- **JSON**: 格式化字符串
- **CSV**: 原始 CSV 文本
- **SQLite**: 表格预览（Schema + 前 10 行）
