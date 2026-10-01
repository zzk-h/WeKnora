## 开工：模块与测试口（seam）

已读 parent STUD-2 与本票 spec，决策已拍板（单文件语法级、CGo 原生 go-tree-sitter、新起语言无关模型），无待确认项，直接开工。

### 要改的模块（新增，不动现有代码）

- `internal/codeparser/types.go` — 语言无关模型：`CodeSymbol`（ID/Kind/Name/File/Range/Signature/Language/Attributes）、`CodeRelation`（From/To/Type：CONTAINS/CALLS/IMPORTS/INHERITS/REFERENCES）、`ParseResult`（符号+关系+单文件错误降级）、`ParseError`。
- `internal/codeparser/parser.go` — `Parser` 接口（`ParseFile(path, src) (*FileResult, error)`、`Language()`）与 `ParseBatch` 批处理入口（单文件失败降级为该文件的错误结果，不中断整批）。
- `internal/codeparser/golang.go` — Go 解析器：go-tree-sitter + tree-sitter-go（CGo 原生，语法编进二进制），抽取 File/Package/Function/Method/Struct/Variable/Import 符号与五种关系（单文件内按名称解析）。
- `go.mod` / `go.sum` — 新增 `github.com/tree-sitter/go-tree-sitter` + `github.com/tree-sitter/tree-sitter-go` 依赖。

### 测试口（seam）

- 公开接口 `codeparser.Parser` / `ParseBatch`：用 Go 代表性样例（含 struct+method+call+import+embed 接口）断言产出的符号集合与关系集合；用语法损坏文件断言单文件错误降级且同批其它文件不受影响。
- 不测内部遍历细节，只断言公开模型字段。

### 环境备注

本机原本无 Go 工具链与 C 编译器，已通过 winget 安装 Go 1.27 与 WinLibs MinGW gcc 16.2（CGo 必需），GOPROXY 切到 goproxy.cn。

状态: in_progress，开始红绿切片
已完成: 上下文阅读（STUD-2 / STUD-8 / 相关评论）、模块与 seam 确认
待确认: 无
