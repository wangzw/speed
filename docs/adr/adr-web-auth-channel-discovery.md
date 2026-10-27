# 前端登录渠道的来源是后端发现端点，前端不写死集合

## 背景

登录面呈现的登录方式集合随部署而变（REQ-signin-004）。此前 `SignInScreen` 的渠道由宿主经 `channels` 属性声明，`SocialSignInSection` 持一个社交渠道名字闭集（`google`、`github`、`wechat`、`dingtalk`、`feishu`），名字到界面的对应写在组件里。后端已把登录方式的发现端点升级为发布每个方法自报的描述符、注册开关与部署级因素种类（[ADR-authn-login-method-discovery](adr-authn-login-method-discovery.md)），`@speed/auth-core` 的 `listMethods()` 返回类型随之改为描述符文档。

前端要在这次接口升级上定下渠道的来源：组件渲染的渠道集合从哪来、名字到界面的映射放在哪、宿主与后端各承担什么。

约束：

- 描述符已给出渲染所需的全部逐方法事实：渲染类别 `kind`（`form`、`redirect`、`enterprise`、`webauthn`）、线协议、品牌标签、首步字段与是否可开户。
- 品牌名（`Google`、`WeChat`）不翻译，由方法带；框架文案走 `@speed/auth-ui` 命名空间的 i18n。
- enterprise 的 IdP 清单按邮箱域 discover，不泄露账户存在性（[ADR-authn-sso](adr-authn-sso.md)），不进发现端点。
- 组件不消费 `@speed/auth-core` 的钩子、只经 `session` 属性驱动会话操作；读取发现文档必须落在既有形态里。

## 考虑过的选项

### 宿主声明渠道闭集

宿主经属性声明要渲染哪些渠道，组件持名字到界面的映射。

**否决**：前端要与后端的方法集合同步维护一张名字到界面的表，新增或停用一个方法都要改前端；名字不是给前端渲染用的对外接口面，把渲染知识建立在其上，等于让两处各自维护同一套方法世界的视图，缓慢漂移。品牌标签与 i18n 也各存一份。

### 复刻 legacy 的公开配置端点

前端另读一份公开的部署配置文档得到渠道与注册开关。

**否决**：要在方法与配置之间再维护一份形状相同的数据源，与方法自报的字段重复，两处必然漂移；配置端点面向的是部署参数，不是「每个方法怎么画」这层逐方法形状。

### 前端逐一调用每个方法的探测接口

每个方法各挂一个公开探测路由，前端逐方法问。

**否决**：N 个方法就是 N 次往返，且每个方法都要新增一处公开只读面；方法与其渲染探测耦合，新增方法就新增一处公开面。

## 决策

采纳：前端登录渠道的来源是后端发现端点。`SignInScreen` 读取 `@speed/auth-core` 的 `listMethods()`（或宿主注入的同一文档），按每个方法描述符的 `kind` 分组渲染；`@speed/auth-core` 的会话契约新增 `discoverSso(email)`，企业入口据此读邮箱域映射的 IdP。前端不写死任何方法名到界面的闭集——`SocialProvider` 由名字闭集改为 `string`，方法显示名取描述符的品牌标签，缺失时按方法名查 `@speed/auth-ui` 命名空间，再退回方法名本身。用户名加口令的自助注册是独立的匿名路由 `POST /api/v1/auth/register`，开户在注册路由完成，不经登录流程；登录流程负责登录，以及社交与短信方法的首登开户（[ADR-authn-signup](adr-authn-signup.md)）。前端把「注册」作为一个入口呈现，可见性由 `discovery.signup.enabled` 与「`discovery.signup.password.enabled` 为真或至少一个可见方法带 `opens_account`」决定。

## 后果

部署新增或停用一个登录方法只改后端，前端不动；每个方法模块持有并自报自己的渲染知识，前端不重抄。发现读取落在既有的会话操作形态里（`session.listMethods()`、`session.discoverSso(email)`），组件仍不消费 auth-core 的钩子；宿主可注入预算好的发现文档（SSR、测试），此时屏幕不发起读取。

**接受的代价：**

- `@speed/auth-core` 的会话契约新增 `discoverSso(email)`，`listMethods()` 的返回类型是描述符文档；两者是宿主可见面的增改。
- 发现文档与企业 discover 是两次读取：前者给出方法集合，后者按域给出 IdP 清单，二者不能合并（IdP 清单不进发现端点）。
- 未知的方法名或类别由前端按描述符兜底渲染（描述符的 `fields` 驱动通用表单、未知 `kind` 不渲染），描述符的取材因此必须覆盖渲染所需。
- 发现读取失败时登录面呈现错误与重试，不再有硬编码的默认渠道可退回。

## 失效条件

- 前端仍要按方法名硬编码某些行为，说明描述符的取材不足以覆盖渲染所需，扩描述符而不是回到硬编码。
- 部署出现无邮箱域可 discover 的企业 SSO，或企业 SSO 需要把提供方清单并入发现，则「IdP 清单不进发现端点、另走 discover」这一条要重审。
- 出现一个既非 `form`／`redirect`／`enterprise`／`webauthn` 又需要前端专门渲染的渲染类别，说明 `kind` 闭集不足以覆盖渲染，扩 `kind` 而不是加旁路。
