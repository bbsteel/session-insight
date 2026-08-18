const githubUrl = "https://github.com/bbsteel/session-insight";

const translations = {
  en: {
    "brand.aria": "Session Insight home",
    "nav.features": "Features",
    "nav.signals": "Signals",
    "nav.workflow": "Workflow",
    "nav.aria": "Primary navigation",
    "nav.github": "Open Session Insight on GitHub",
    "language.aria": "Switch to Chinese",
    "hero.visualAria": "Session Insight interface preview",
    "hero.actualUi": "ACTUAL UI / DARK THEME",
    "hero.actualUiStatus": "REAL PRODUCT VIEW",
    "hero.actualUiCaption": "The actual Session Insight interface.",
    "hero.actualUiNote": "Captured from a real local session.",
    "hero.eyebrow": "LOCAL-FIRST OBSERVABILITY FOR AI CODING SESSIONS",
    "hero.headingLead": "See what your",
    "hero.headingAccent": "coding agents",
    "hero.headingTail": "actually did.",
    "hero.body": "Replay the work, follow the evidence, and understand the decisions behind every agent session — on your machine.",
    "hero.ctaGithub": "Explore on GitHub",
    "hero.ctaRelease": "Get v0.7.1",
    "hero.note": "Open source · local data · built for long sessions",
    "strip.replay": "REPLAY THE CONTEXT",
    "strip.evidence": "FOLLOW THE EVIDENCE",
    "strip.collaboration": "SEE THE COLLABORATION",
    "features.eyebrow": "THE INSIGHT LAYER",
    "features.title": "From raw transcript to usable signal.",
    "features.body": "Your agent did more than print messages. Session Insight turns the whole run into a navigable, inspectable record.",
    "feature.replay.label": "NAVIGATION",
    "feature.replay.title": "Replay the moment that matters.",
    "feature.replay.body": "Search, jump, fold, and follow the exact turn where the plan changed — without losing your place in a long session.",
    "feature.evidence.label": "PROVENANCE",
    "feature.evidence.title": "Know what actually happened.",
    "feature.evidence.body": "Connect tool calls, files, commits, change requests, and approvals into an evidence trail you can inspect.",
    "feature.collaboration.label": "COLLABORATION",
    "feature.collaboration.title": "See the agents behind the agent.",
    "feature.collaboration.body": "Make child agents, tasks, handoffs, and parallel work visible in one timeline.",
    "signals.eyebrow": "A SIGNAL, NOT ANOTHER CHAT WINDOW",
    "signals.title": "Read the shape of the work.",
    "signals.body": "A session is a system: context pressure, repeated work, risky actions, useful evidence, and decisions over time.",
    "strip.aria": "Session Insight principles",
    "screen.replay.alt": "Session Insight replay view",
    "screen.replay.index": "REPLAY / 01",
    "screen.replay.caption": "Navigate the entire conversation without losing the thread.",
    "screen.analytics.alt": "Session Insight analytics view",
    "screen.analytics.index": "ANALYTICS / 02",
    "screen.analytics.caption": "Find pressure, cost, anomalies, and patterns in the run.",
    "screen.interaction.alt": "Session Insight interaction view",
    "screen.interaction.index": "EVIDENCE / 03",
    "screen.interaction.caption": "Keep the human and agent decisions in the same frame.",
    "screen.zoomAria": "Open screenshot full size",
    "screen.dialogLabel": "SCREENSHOT / FULL SIZE",
    "screen.closeAria": "Close full-size screenshot",
    "workflow.eyebrow": "DESIGNED FOR THE LONG RUN",
    "workflow.title": "Stay oriented when the session gets serious.",
    "workflow.body": "Use it after the magic wears off — when the context is huge, the agent has branched, and you need to know what to trust.",
    "workflow.one.title": "Start with the whole run.",
    "workflow.one.body": "Index local sessions and get a fast map of turns, tools, files, and active agents.",
    "workflow.two.title": "Zoom into the decision.",
    "workflow.two.body": "Search exact terms, jump to first or last matches, and anchor the viewport to the evidence.",
    "workflow.three.title": "Leave with a trace.",
    "workflow.three.body": "Connect the outcome to commits, pull requests, reviews, and the people or agents involved.",
    "workflow.asideLabel": "ONE COMMAND TO START",
    "workflow.asideCopy": "No hosted account. No upload step. Just a local window into the work already on your machine.",
    "local.eyebrow": "LOCAL BY DESIGN",
    "local.title": "Your sessions stay yours.",
    "local.body": "Session Insight is built as a local developer tool. It reads the session roots you choose, builds a local index, and gives you an inspection layer without asking you to move your work into another cloud.",
    "local.pointOne": "Local session roots",
    "local.pointTwo": "Read-only evidence layer",
    "local.pointThree": "Open source workflow",
    "cta.eyebrow": "THE NEXT SESSION IS ALREADY WAITING",
    "cta.title": "Make the invisible work legible.",
    "cta.body": "Start with the source. Keep the data local. See the signal.",
    "cta.github": "Open the repository",
    "cta.release": "Read the latest release",
    "footer.tagline": "A clearer view of the work behind the prompt.",
    "footer.github": "GitHub",
    "footer.releases": "Releases",
    "footer.top": "Back to top ↑",
    "footer.build": "OPEN SOURCE / LOCAL-FIRST / 2026",
    "page.title": "Session Insight — See what your coding agents actually did.",
  },
  zh: {
    "brand.aria": "Session Insight 首页",
    "nav.features": "能力",
    "nav.signals": "信号",
    "nav.workflow": "工作流",
    "nav.aria": "主导航",
    "nav.github": "在 GitHub 打开 Session Insight",
    "language.aria": "切换到英文",
    "hero.visualAria": "Session Insight 界面预览",
    "hero.actualUi": "真实界面 / 深色主题",
    "hero.actualUiStatus": "真实产品视图",
    "hero.actualUiCaption": "真实的 Session Insight 界面。",
    "hero.actualUiNote": "来自真实本地会话的脱敏截图。",
    "hero.eyebrow": "面向 AI 编程会话的本地优先观测层",
    "hero.headingLead": "看清你的",
    "hero.headingAccent": "编程代理",
    "hero.headingTail": "到底做了什么。",
    "hero.body": "重放过程、追踪证据、理解每一次代理会话背后的决策——全部留在你的机器上。",
    "hero.ctaGithub": "前往 GitHub",
    "hero.ctaRelease": "获取 v0.7.1",
    "hero.note": "开源 · 本地数据 · 为长会话而生",
    "strip.replay": "重放上下文",
    "strip.evidence": "追踪证据",
    "strip.collaboration": "看见协作",
    "features.eyebrow": "洞察层",
    "features.title": "从原始记录，到真正可用的信号。",
    "features.body": "代理做的不只是输出消息。Session Insight 把整段运行变成可导航、可检查的记录。",
    "feature.replay.label": "导航",
    "feature.replay.title": "重放真正重要的那一刻。",
    "feature.replay.body": "搜索、跳转、折叠，准确回到计划发生变化的那一轮，同时不丢失长会话中的阅读位置。",
    "feature.evidence.label": "溯源",
    "feature.evidence.title": "知道到底发生了什么。",
    "feature.evidence.body": "把工具调用、文件、提交、变更请求和审批串成一条可检查的证据链。",
    "feature.collaboration.label": "协作",
    "feature.collaboration.title": "看见代理背后的代理。",
    "feature.collaboration.body": "让子代理、任务、交接和并行工作都出现在同一条时间线上。",
    "signals.eyebrow": "不是另一个聊天窗口，而是工作信号",
    "signals.title": "读懂工作的形状。",
    "signals.body": "一次会话是一个系统：上下文压力、重复工作、风险操作、有用证据，以及随时间发生的决策。",
    "strip.aria": "Session Insight 的核心原则",
    "screen.replay.alt": "Session Insight 重放视图",
    "screen.replay.index": "重放 / 01",
    "screen.replay.caption": "浏览完整对话，同时不丢失主线。",
    "screen.analytics.alt": "Session Insight 分析视图",
    "screen.analytics.index": "分析 / 02",
    "screen.analytics.caption": "发现会话中的压力、成本、异常和模式。",
    "screen.interaction.alt": "Session Insight 交互视图",
    "screen.interaction.index": "证据 / 03",
    "screen.interaction.caption": "把人与代理的决策放在同一幅图里。",
    "screen.zoomAria": "打开截图大图",
    "screen.dialogLabel": "截图 / 原始尺寸",
    "screen.closeAria": "关闭截图大图",
    "workflow.eyebrow": "为长期运行而设计",
    "workflow.title": "会话变复杂时，依然保持方向感。",
    "workflow.body": "当上下文变得庞大、代理开始分支，而你需要知道什么值得信任时，它才真正派上用场。",
    "workflow.one.title": "先看完整运行。",
    "workflow.one.body": "索引本地会话，快速得到轮次、工具、文件和活跃代理的全貌。",
    "workflow.two.title": "再深入那个决定。",
    "workflow.two.body": "搜索精确术语，跳到首次或末次匹配，把视口锚定到证据。",
    "workflow.three.title": "最后留下追踪线索。",
    "workflow.three.body": "把结果连接到提交、PR、评审，以及参与其中的人或代理。",
    "workflow.asideLabel": "一个命令，立即开始",
    "workflow.asideCopy": "不需要托管账号，也没有上传步骤。只是在本地打开一扇窗口，看见机器上已经发生的工作。",
    "local.eyebrow": "本地优先",
    "local.title": "你的会话，始终属于你。",
    "local.body": "Session Insight 是一个本地开发工具。它读取你选择的会话目录，建立本地索引，并提供检查层，不要求你把工作搬到另一个云端。",
    "local.pointOne": "本地会话目录",
    "local.pointTwo": "只读证据层",
    "local.pointThree": "开源工作流",
    "cta.eyebrow": "下一个会话已经在等待",
    "cta.title": "让不可见的工作变得清晰。",
    "cta.body": "从源码开始。让数据留在本地。看见真正的信号。",
    "cta.github": "打开代码仓库",
    "cta.release": "查看最新版本",
    "footer.tagline": "更清晰地看见 prompt 背后的工作。",
    "footer.github": "GitHub",
    "footer.releases": "版本发布",
    "footer.top": "回到顶部 ↑",
    "footer.build": "开源 / 本地优先 / 2026",
    "page.title": "Session Insight — 看清 AI 编程代理到底做了什么。",
  },
};

const languageToggle = document.querySelector("#language-toggle");
const languageLabel = document.querySelector("[data-language-label]");
const screenshotDialog = document.querySelector("#screenshot-dialog");
const screenshotDialogImage = document.querySelector("#screenshot-dialog-image");
const screenshotDialogClose = document.querySelector("#screenshot-dialog-close");
let activeScreenshotName = "";

function applyLanguage(language) {
  const selectedLanguage = language === "zh" ? "zh" : "en";
  const copy = translations[selectedLanguage];
  document.documentElement.lang = selectedLanguage === "zh" ? "zh-CN" : "en";
  document.documentElement.dataset.language = selectedLanguage;
  document.title = copy["page.title"];

  document.querySelectorAll("[data-i18n]").forEach((element) => {
    const translation = copy[element.dataset.i18n];
    if (translation) element.textContent = translation;
  });

  document.querySelectorAll("[data-i18n-attr]").forEach((element) => {
    const [attribute, key] = element.dataset.i18nAttr.split(":");
    const translation = copy[key];
    if (translation) element.setAttribute(attribute, translation);
  });

  const screenshotLocale = selectedLanguage === "zh" ? "zh-CN" : "en";
  document.querySelectorAll("[data-locale-src]").forEach((image) => {
    const screenshotName = image.dataset.localeSrc;
    image.src = `./assets/screenshots/${screenshotLocale}/${screenshotName}.png`;
  });

  if (activeScreenshotName && screenshotDialog?.open) {
    screenshotDialogImage.src = `./assets/screenshots/${screenshotLocale}/${activeScreenshotName}.png`;
  }

  languageLabel.textContent = selectedLanguage === "zh" ? "EN" : "中文";
  languageToggle.setAttribute("aria-pressed", String(selectedLanguage === "zh"));
  localStorage.setItem("session-insight-language", selectedLanguage);
}

languageToggle.addEventListener("click", () => {
  const nextLanguage = document.documentElement.dataset.language === "zh" ? "en" : "zh";
  applyLanguage(nextLanguage);
});

const savedLanguage = localStorage.getItem("session-insight-language");
applyLanguage(savedLanguage === "zh" ? "zh" : "en");

document.querySelectorAll('a[href^="#"]').forEach((anchor) => {
  anchor.addEventListener("click", (event) => {
    const targetId = anchor.getAttribute("href");
    if (!targetId || targetId === "#") return;
    const target = document.querySelector(targetId);
    if (!target) return;
    event.preventDefault();
    target.scrollIntoView({ behavior: "smooth", block: "start" });
  });
});

document.querySelectorAll(`a[href="${githubUrl}"]`).forEach((link) => {
  link.dataset.destination = "github";
});

document.querySelectorAll("[data-screenshot-zoom]").forEach((trigger) => {
  trigger.addEventListener("click", () => {
    const screenshotImage = trigger.querySelector("img");
    if (!screenshotImage || !screenshotDialog || !screenshotDialogImage) return;
    activeScreenshotName = screenshotImage.dataset.localeSrc ?? "";
    screenshotDialogImage.src = screenshotImage.src;
    screenshotDialogImage.alt = screenshotImage.alt;
    screenshotDialog.showModal();
  });
});

screenshotDialogClose?.addEventListener("click", () => screenshotDialog?.close());
screenshotDialog?.addEventListener("click", (event) => {
  if (event.target === screenshotDialog) screenshotDialog.close();
});
screenshotDialog?.addEventListener("close", () => {
  activeScreenshotName = "";
  screenshotDialogImage.removeAttribute("src");
});
