import {
    ChartNoAxesColumn,
    CircleDollarSign,
    Clapperboard,
    Compass,
    Image as ImageIcon,
    MessagesSquare,
    Repeat2,
    Signature,
    Sparkles,
    Target,
    Users,
    Video,
    WandSparkles,
    type LucideIcon,
} from "lucide-react";

import type { CreationMode } from "./creation-types";

/**
 * 首页业务板块。
 *
 * mode 对应生成能力类型，dispatch 决定点击板块后是否直接展开输入区，
 * skill 为可选的技能提示词前缀（按需在后续阶段接入技能绑定）。
 */
export type CreationBoardLane = "realtime" | "inline_skill" | "slow_lane";

export type CreationBoard = {
    key: string;
    label: string;
    description: string;
    category: "content" | "analysis" | "decision" | "business" | "identity" | "legal" | "general";
    lane: CreationBoardLane;
    mode: CreationMode;
    icon: LucideIcon;
    prompt: string;
    /** 需要先选参考素材才能推进的板块（如视频复刻）。 */
    needsAsset?: boolean;
};

export const creationBoardCategoryLabels: Record<CreationBoard["category"] | "all", string> = {
    all: "全部板块",
    content: "内容创作",
    analysis: "分析洞察",
    decision: "决策参谋",
    business: "经营增长",
    identity: "自我定位",
    legal: "法务合规",
    general: "随手一问",
};

export const creationBoardLaneLabels: Record<CreationBoardLane, string> = {
    realtime: "实时",
    inline_skill: "技能增强",
    slow_lane: "深度任务",
};

export const creationBoards: CreationBoard[] = [
    {
        key: "oral-script",
        label: "写口播脚本",
        description: "高转化口播、钩子开头与结尾收口",
        category: "content",
        lane: "realtime",
        mode: "text",
        icon: MessagesSquare,
        prompt: "请为这条短视频写一份可直接拍摄的口播脚本：先给3 个不同的钩子开头备选，再写主体段落，最后给一句引导互动的收尾。整体口语化，控制在 60 秒以内。",
    },
    {
        key: "image-gen",
        label: "生图",
        description: "海报、主图与宣传素材",
        category: "content",
        lane: "inline_skill",
        mode: "image",
        icon: ImageIcon,
        prompt: "生成一张宣传主图：主体居中、留出标题区域，配色克制、材质真实，竖版构图。",
    },
    {
        key: "video-gen",
        label: "生视频",
        description: "短视频片段与视觉成片",
        category: "content",
        lane: "inline_skill",
        mode: "video",
        icon: Video,
        prompt: "生成一个 5 秒的视频片段：雨夜城市街口，霓虹灯倒映在湿润路面，镜头从远景缓慢推进，电影感光影，16:9 横屏。",
    },
    {
        key: "video-replica",
        label: "视频复刻",
        description: "发抖音/ 小红书链接，复刻成你的版本",
        category: "content",
        lane: "inline_skill",
        mode: "video",
        icon: Repeat2,
        needsAsset: true,
        prompt: "我上传了一条参考视频，请先拆解它的镜头表、逐字稿、音频节奏和关键指标，再给出可复刻的生成方案。",
    },
    {
        key: "short-drama",
        label: "AI 短剧剧本",
        description: "进入短剧剧本创作流程",
        category: "content",
        lane: "realtime",
        mode: "text",
        icon: Clapperboard,
        prompt: "帮我写一集竖屏短剧剧本：先给一句话梗概和人物关系小传，再写分场剧本，每场标注场景、时间、情绪和台词。",
    },
    {
        key: "decision",
        label: "做决策",
        description: "定价、渠道、团队等重大决定",
        category: "decision",
        lane: "inline_skill",
        mode: "text",
        icon: Target,
        prompt: "我正在做一个重要决定，先帮我梳理清楚：我有哪些选项、各自的底线和风险是什么？先给信息和筛选，最后的选择由我自己定。",
    },
    {
        key: "acquisition",
        label: "行业获客方案",
        description: "分析行业切口与获客打法",
        category: "analysis",
        lane: "slow_lane",
        mode: "text",
        icon: ChartNoAxesColumn,
        prompt: "帮我做一份行业获客方案：先分析这个行业真正的付费人群和切口在哪，再给出 3 条可落地的获客路径，每条说明投入、周期和预期转化。",
    },
    {
        key: "positioning",
        label: "战略定位分析",
        description: "竞争位置、取舍边界与下一阶段路线",
        category: "analysis",
        lane: "inline_skill",
        mode: "text",
        icon: Compass,
        prompt: "帮我做战略定位分析：我的核心竞争力是什么、竞争位置在哪、要放弃什么。请搭出框架，让我自己在框架里填入业务数据。",
    },
    {
        key: "business-model",
        label: "商业模型",
        description: "梳理收入结构与盈利逻辑",
        category: "business",
        lane: "slow_lane",
        mode: "text",
        icon: CircleDollarSign,
        prompt: "帮我梳理商业模型：收入从哪来、成本在哪、毛利结构如何、现金流节奏怎样。先给一个需要我填数的框架，再逐项推导。",
    },
    {
        key: "team",
        label: "团队管理方案",
        description: "分工、激励与冲突处理",
        category: "business",
        lane: "inline_skill",
        mode: "text",
        icon: Users,
        prompt: "帮我设计团队管理方案：先梳理角色分工与责任边界，再给激励规则和冲突处理机制，指出哪些必须由管理者自己拍板。",
    },
    {
        key: "legal",
        label: "法律合同",
        description: "合同起草与条款审查",
        category: "legal",
        lane: "inline_skill",
        mode: "text",
        icon: Signature,
        prompt: "帮我审阅这份合同：逐条列出对甲方有利的条款、可能踩坑的条款和缺失条款，并给出修改建议。",
    },
    {
        key: "talent",
        label: "天赋探索",
        description: "发现核心天赋与身份定位",
        category: "identity",
        lane: "inline_skill",
        mode: "text",
        icon: Sparkles,
        prompt: "帮我做天赋探索：先问我几个能暴露真实偏好的问题，再基于我的经历归纳核心天赋和身份定位，不要给笼统的星座式结论。",
    },
    {
        key: "ask",
        label: "随便聊聊",
        description: "有什么问题直接说",
        category: "general",
        lane: "realtime",
        mode: "text",
        icon: WandSparkles,
        prompt: "",
    },
];
