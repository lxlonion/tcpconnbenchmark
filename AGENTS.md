# AGENTS.md

## Working priority

All work is first triaged internally by verifiable evidence, actual scope of impact, and recoverability, then ordered and resourced: P0 = severe, widespread, or unrecoverable harm to core goals, security, funds, or permissions — handle immediately. P1 = core goals, key functionality, or actual delivery substantially blocked — fix first, but not automatically "urgent". P2 = local or minor divergence that does not block core goals — verify at low priority, batch, or defer. P3 = cosmetic, typographic, or negligible impact — normally do not act. Triage is for trade-offs, not for emitting a priority label for every small item.

## 子代理（subagent）使用规则

任务分步骤、按领域或模块划分

以下情况委派子代理：
1. 上下文膨胀：需要通读大量材料（长文档、规范、大型日志、陌生代码库），编写目标清晰的代码
2. 独立并行：几项互不依赖的工作可以同时做，各自用新的上下文，做完即丢。
3. 独立验证：需要一个没看过实现过程的代理，对结果做黑盒检查。

委派时：
- 按上下文切分：每个子代理拿到一块几乎不依赖其他部分的工作，并交回简短结果。不要按问题类型拆分后让多个子代理来回传话。
- 子代理从空白上下文开始：提示中写清目标、相关路径、约束（包括本文件中适用的规则）和完成标准，不要假设它知道对话内容。
- 要求它返回结果：结论、证据（文件路径 / 命令 / 数值）、未验证或不确定的部分。不要返回过程叙述, 但如果是编码任务可以多讲工程实现的细节。
- 主代理负责核对：关键结论要对照真实文件或命令输出验证后才采用，最终结论和与用户的对话始终由主代理负责。
- 优先使用现有子代理，按其描述选择。没有充分理由不要新建子代理定义。

