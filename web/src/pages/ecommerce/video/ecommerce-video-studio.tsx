import { useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";

import { SegmentedControl } from "@/components/ui/base/segmented-control/segmented-control";

import { useEcommerceVideo } from "./use-ecommerce-video";
import { EcommerceVideoComposer } from "./video-composer";
import { EcommerceVideoResults } from "./video-results";
import type { EcommerceWorkRemix } from "./use-ecommerce-video";
import "./ecommerce-video.css";

/**
 * 电商 Agent「商品视频」：与「商品图片」同构——左侧作品区、右侧创作面板。
 * .ecs-shell 的网格是 `minmax(0,1fr) minmax(380px,420px)`，第一列必须是作品区（.commerce-output-gallery），
 * 第二列才是 .ecs-composer；顺序写反会把参数面板撑满大区、作品栏挤成右侧窄条。
 * 顶部的「商品图片 / 商品视频」切换由页面传入。
 */
export default function EcommerceVideoStudio({ kindSwitch, hidden = false }: { kindSwitch: ReactNode; hidden?: boolean }) {
    const { t } = useTranslation();
    const state = useEcommerceVideo();
    const location = useLocation();
    const navigate = useNavigate();
    useEffect(() => {
        const work = (location.state as { workRemix?: EcommerceWorkRemix } | null)?.workRemix;
        if (work?.materials?.length) {
            state.applyWorkRemix(work);
            navigate(location.pathname + location.search, { replace: true, state: null });
            return;
        }
        const incoming = (location.state as { workPrompt?: unknown } | null)?.workPrompt;
        if (typeof incoming !== "string" || !incoming.trim()) return;
        state.setNotes(incoming.slice(0, 6000));
        navigate(location.pathname + location.search, { replace: true, state: null });
    }, [location.key]);
    const [mobileView, setMobileView] = useState<"create" | "works">("create");
    const running = state.projects.filter((project) => ["analyzing", "generating", "composing"].includes(project.status)).length;
    return (
        <div className="ecs-shell ecv-shell" data-mobile-view={mobileView} style={hidden ? { display: "none" } : undefined} aria-hidden={hidden || undefined}>
            <div className="ecs-mobile-tabs">
                <SegmentedControl<"create" | "works">
                    block
                    ariaLabel={t("ecommerceVideo.studio.switchLabel")}
                    value={mobileView}
                    onChange={setMobileView}
                    options={[
                        { value: "create", label: t("ecommerceVideo.studio.create") },
                        { value: "works", label: running ? t("ecommerceVideo.studio.worksRunning", { count: running }) : t("ecommerceVideo.studio.works") },
                    ]}
                />
            </div>
            <EcommerceVideoResults state={state} />
            <aside className="ecs-composer" aria-label={t("ecommerceVideo.studio.composerLabel")}>
                <header className="ecs-composer-head">{kindSwitch}</header>
                <EcommerceVideoComposer state={state} />
            </aside>
        </div>
    );
}
