---
type: concept
title: LLVM 的模块化架构
tags:
  - LLVM
  - 编译器
aliases:
  - LLVM architecture
---
# LLVM 的模块化架构

LLVM 通过稳定的中间表示将语言前端、优化流水线和目标后端解耦。这一边界使多个语言和硬件目标能够复用同一套优化与代码生成基础设施。

## 核心结论

稳定的 IR 是 LLVM 组件复用与跨语言支持的关键边界。
