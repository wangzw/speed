# API origin 在运行期注入，不在构建期烘焙

## 背景

一份 `server/dist` 要在合一拓扑与分离拓扑下共用（[ADR-web-static-hosting](adr-web-static-hosting.md)、[ADR-web-two-build-artifacts](adr-web-two-build-artifacts.md)）：合一拓扑里产物内嵌进 Go 二进制、由本进程答出；分离拓扑里同一份产物交静态主机、后端只做 API。

API 的 origin 因部署而异：与页面同源、在另一台主机、经反向代理挂在一条路径前缀下。今天的 origin 在装配期定下——`web/packages/product-shell/src/bootstrap.tsx` 的 `bootstrapSpeedApp` 取 `definition.baseUrl`，缺省 `window.location.origin`；一个不同源的部署要在应用源码里写死，或经构建期注入（vite 的 `import.meta.env`，[REF-vite](../references.md)）。

把 origin 烘进 dist，同一份 dist 就只在一种 origin 下可用：改部署位置要重建前端，合一与分离两条拓扑也各需一次构建。

## 考虑过的选项

### 构建期烘焙 origin

在构建时把 origin 注入产物（vite 的环境变量或 `define`）。

**否决**：一份 dist 只在一种 origin 下可用；两条拓扑各需一次构建，与 [ADR-web-two-build-artifacts](adr-web-two-build-artifacts.md) 的「拓扑共用同一份产物」直接冲突，且改一次部署位置要重建并重发前端。

### 由宿主在应用源码里写死 origin

在应用入口把 origin 写成常量交给 `bootstrapSpeedApp`。

**否决**：同构建期烘焙的后果——origin 成为一份需重新发布的产物的一部分；「另一台主机、另一条反代前缀」这类运行期事实被固化进源码。

### 运行期注入：dist 旁的 runtime-config.json 在启动时取回，缺省回退同源

**采纳**，见决策。

## 决策

采纳：**API origin 在运行期注入，经入口文档旁的 `runtime-config.json` 取回，按相对路径取、缺省回退同源；不在构建期烘焙。**

- dist 不携带 origin；同一份 dist 在合一拓扑与分离拓扑下共用。
- 部署时改 origin 只改 `runtime-config.json`，不重建前端。
- 取回那份文件失败、或其中没有 origin 时，回退到同源（`window.location.origin`）。

## 后果

一份 dist 跨部署复用，改部署拓扑不必重建前端；origin 与部署位置一致。

**接受的代价：**

- **首屏多一次取回配置的往返。** 应用要等 `runtime-config.json` 取回、或取回失败回退之后，才能发第一个 API 请求；这段等待每个部署都要付。
- **多一份随 dist 部署的文件。** 内嵌装配与静态托管装配都要保证 `runtime-config.json` 与产物一起就位；缺失时只剩同源回退，一个跨源的部署会静默打向同源并失败，而不是在启动时报出缺配置。
- **相对路径取把配置文件钉在与入口文档同前缀处。** 把前端挂到一条子路径下、把配置放在别处，会取不到；配置的位置只能与入口文档同处一地。

## 失效条件

- 若实践中跨源部署几乎不出现，这一次取回与回退就是纯代价，应退回同源缺省并删掉注入机制。
- 若静态主机的部署约定改为在答出入口文档时把 origin 内联进 HTML，相对路径取的那份文件可以省去。
