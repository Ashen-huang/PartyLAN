# PartyLAN 设置语言切换设计

## 1. 背景

当前前端文案分散在多个 React 组件中，并且中英文混杂。设置面板已经承担网络、诊断叠层等偏好入口，因此语言切换也应放在设置中。

本轮目标是让用户可以在 `中文` 与 `English` 之间切换主要界面语言，并为后续继续补全文案提供一个轻量 i18n 结构。

## 2. 目标

- 在设置面板中新增语言选择控件。
- 支持 `中文` 与 `English` 两种语言。
- 切换后主要前端界面立即更新。
- 语言偏好保存到 `localStorage`，下次启动继续使用。
- 优先覆盖高频 UI 文案，减少当前中英文混杂。

## 3. 非目标

- 不翻译 Go 后端返回的错误字符串。
- 不重构后端错误码体系。
- 不接入第三方 i18n 库。
- 不支持超过中文和英文的语言列表。
- 不把语言偏好写入 Go 配置文件。

## 4. 推荐方案

新增 `frontend/src/i18n.ts`，提供：

- `Language = 'zh' | 'en'`
- `DEFAULT_LANGUAGE = 'zh'`
- `LANGUAGE_STORAGE_KEY`
- `createTranslator(language)`
- `languageOptions`
- 翻译字典

`App.tsx` 维护当前语言状态，读取与写入 `localStorage`。语言和翻译函数通过 props 传给主要组件。

## 5. 覆盖范围

第一轮覆盖以下组件的主要静态文案：

- `SettingsPanel`
- `StatusBar`
- `RoomControls`
- `MemberList`
- `AvailableNodes`
- `RoomToolsDialog`
- `ScreenPreview`
- `DiagnosticsPanel`
- `App.tsx` 中大厅欢迎区、按钮 title、提示列表

后端错误、日志、运行时健康检查原文、DERP health 原文保持原样。

## 6. 交互设计

设置面板新增 `Language / 语言` 字段。

中文界面中显示：

- 字段标题：`语言`
- 选项：`中文`、`English`

英文界面中显示：

- 字段标题：`Language`
- 选项：`中文`、`English`

切换行为：

1. 用户在设置面板选择语言。
2. `App.tsx` 更新语言状态。
3. 写入 `localStorage`。
4. 当前打开的设置面板与主界面立即刷新文案。

## 7. 数据流

启动时：

1. `App.tsx` 从 `localStorage` 读取语言。
2. 如果值不是 `zh` 或 `en`，回退为 `zh`。
3. 创建 `t` 函数。
4. 将语言与 `t` 传给组件。

运行时：

1. `SettingsPanel` 触发 `onLanguageChange(language)`。
2. `App.tsx` 更新状态并持久化。
3. React 重新渲染使用 `t` 的组件。

## 8. 技术约束

- 不引入新依赖。
- 翻译 key 使用稳定字符串，例如 `settings.title`、`room.startSharing`。
- 对带变量的文案使用小函数或简单参数替换。
- 动态运行时值，例如 room id、版本号、网络 health 原文，不翻译内容本身。

## 9. 错误处理

- `localStorage` 读取失败时使用中文。
- `localStorage` 写入失败时只影响持久化，不阻止当前语言切换。
- 缺失翻译 key 时回退显示 key，便于开发时发现遗漏。

## 10. 验收标准

- 设置中可以切换中文和英文。
- 切换后主界面和设置面板立即更新语言。
- 刷新或重启前端后保留上次语言选择。
- 高频 UI 不再明显中英文混杂。
- `npm run build` 通过。
- `go test ./...` 通过或明确说明未受影响。
