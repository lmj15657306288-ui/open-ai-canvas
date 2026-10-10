import { useMemo, useState } from "react";
import { LayoutGroup, motion, useReducedMotion } from "motion/react";

import { aceternityMotion } from "@/lib/aceternity-motion";
import { creationBoardCategoryLabels, creationBoardLaneLabels, creationBoards, type CreationBoard, type CreationBoardLane } from "./creation-boards";

/** 板块分类的展示顺序：内容与分析优先，决策与经营次之，自我定位与法务再次，随手一问收尾。 */
const categoryOrder: Array<CreationBoard["category"] | "all"> = ["all", "content", "analysis", "decision", "business", "identity", "legal", "general"];

const laneDotClass: Record<CreationBoardLane, string> = {
    realtime: "is-realtime",
    inline_skill: "is-skill",
    slow_lane: "is-deep",
};

export function CreationBoards({ onSelect, brandName }: { onSelect: (board: CreationBoard) => void; brandName: string }) {
    const reducedMotion = useReducedMotion();
    const [category, setCategory] = useState<CreationBoard["category"] | "all">("all");

    const categories = useMemo(
        () => categoryOrder.filter((value) => value === "all" || creationBoards.some((board) => board.category === value)),
        [],
    );
    const boards = useMemo(() => (category === "all" ? creationBoards : creationBoards.filter((board) => board.category === category)), [category]);

    return <section className="creation-boards" aria-labelledby="creation-boards-title">
        <div className="creation-boards-heading">
            <h2 id="creation-boards-title">开始一个板块</h2>
            <p>选板块后{brandName}会自动切到对应能力，并填好引导提示词</p>
        </div>
        <div className="creation-board-filters" role="group" aria-label="板块分类">
            {categories.map((value) => {
                const count = value === "all" ? creationBoards.length : creationBoards.filter((board) => board.category === value).length;
                if (!count) return null;
                return <button
                    key={value}
                    type="button"
                    aria-pressed={category === value}
                    onClick={() => setCategory(value)}
                >
                    {creationBoardCategoryLabels[value]}
                    <span>{count}</span>
                </button>;
            })}
        </div>
        <LayoutGroup id="creation-board-grid">
            <motion.div className="creation-board-grid" layout transition={reducedMotion ? { duration: 0 } : aceternityMotion.spring.surface}>
                {boards.map((board) => {
                    const Icon = board.icon;
                    return <motion.button
                        key={board.key}
                        type="button"
                        layout
                        layoutId={`creation-board-${board.key}`}
                        className="creation-board-card"
                        onClick={() => onSelect(board)}
                        whileHover={reducedMotion ? undefined : { y: -2 }}
                        transition={aceternityMotion.spring.surface}
                    >
                        <span className="creation-board-icon"><Icon size={17} strokeWidth={2} /></span>
                        <span className="creation-board-copy">
                            <strong>{board.label}</strong>
                            <span>{board.description}</span>
                        </span>
                        <span className={`creation-board-lane ${laneDotClass[board.lane]}`}>
                            {board.needsAsset ? "需参考素材" : creationBoardLaneLabels[board.lane]}
                        </span>
                    </motion.button>;
                })}
            </motion.div>
        </LayoutGroup>
    </section>;
}
