/**
 * Strain Lineage Tree Renderer
 * Renders a collapsible ancestry tree with Wikipedia-style blue/red links.
 * Shows the current strain as the root, with parents nested below.
 * Blue link = strain exists in the database (clickable)
 * Red link = strain not yet added (links to add-strain flow)
 */
document.addEventListener("DOMContentLoaded", () => {
    const treeContainer = document.getElementById("lineageTree");
    const emptyMsg = document.getElementById("lineageEmpty");
    const lineageCard = document.getElementById("lineageCard");

    if (!treeContainer || !lineageCard || typeof currentStrainID === "undefined") return;

    const opts = lineageCard.dataset;
    // With a source lineage note shown below, an empty tree isn't "no lineage".
    const showEmpty = () => { if (!opts.hasNote) emptyMsg.style.display = "block"; };

    fetch(`/strains/${currentStrainID}/lineage`, { cache: "no-store" })
        .then(res => res.json())
        .then(lineageData => {
            if (!lineageData || lineageData.length === 0) {
                showEmpty();
                return;
            }

            // Build wrapper: current strain as root with parents as children
            const rootUl = document.createElement("ul");
            rootUl.className = "lineage-tree";

            const rootLi = document.createElement("li");
            rootLi.className = "lineage-node";

            const rootName = document.createElement("span");
            rootName.className = "lineage-name";

            // Current strain is bold, not a link (we're already on its page)
            const label = document.createElement("strong");
            label.textContent = currentStrainName;
            label.style.color = "var(--color-text)";
            rootName.appendChild(label);

            // Add collapse toggle
            const toggle = document.createElement("button");
            toggle.className = "lineage-toggle";
            toggle.innerHTML = '<i class="fa-solid fa-chevron-down fa-xs"></i>';
            toggle.title = opts.showAncestry || "";
            rootName.insertBefore(toggle, rootName.firstChild);

            rootLi.appendChild(rootName);

            // Build the parent tree beneath the root
            const parentTree = buildTree(lineageData, opts);
            parentTree.className += " lineage-subtree";
            rootLi.appendChild(parentTree);

            toggle.addEventListener("click", () => {
                const isCollapsed = parentTree.classList.toggle("collapsed");
                toggle.innerHTML = isCollapsed
                    ? '<i class="fa-solid fa-chevron-right fa-xs"></i>'
                    : '<i class="fa-solid fa-chevron-down fa-xs"></i>';
            });

            rootUl.appendChild(rootLi);
            treeContainer.appendChild(rootUl);
        })
        .catch(err => {
            console.error("Failed to load lineage:", err);
            showEmpty();
        });
});

function buildTree(parents, opts) {
    const ul = document.createElement("ul");
    ul.className = "lineage-tree";

    parents.forEach(parent => {
        const li = document.createElement("li");
        li.className = "lineage-node";

        const nameSpan = document.createElement("span");
        nameSpan.className = "lineage-name";

        if (parent.parent_strain_id) {
            // Blue link - strain exists
            const link = document.createElement("a");
            link.href = `/strain/${parent.parent_strain_id}`;
            link.className = "lineage-link-exists";
            link.textContent = parent.parent_name;
            nameSpan.appendChild(link);
        } else {
            // Red link - strain doesn't exist yet, click to add
            const link = document.createElement("a");
            link.href = `/strain/new?name=${encodeURIComponent(parent.parent_name)}`;
            link.className = "lineage-link-missing";
            link.title = (opts.addAsStrain || "{name}").replace("{name}", parent.parent_name);
            link.textContent = parent.parent_name;
            nameSpan.appendChild(link);
        }

        // StrainCompass has no per-parent ids, so link to its search for the name
        // and let the user pick among the matches.
        if (opts.straincompass) {
            const ext = document.createElement("a");
            ext.href = "https://straincompass.com/en/strains?q=" + encodeURIComponent(parent.parent_name);
            ext.target = "_blank";
            ext.rel = "noopener noreferrer";
            ext.className = "lineage-ext-link ms-1 text-muted";
            ext.title = (opts.searchStraincompass || "{name}").replace("{name}", parent.parent_name);
            ext.setAttribute("aria-label", ext.title);
            ext.innerHTML = '<i class="fa-solid fa-arrow-up-right-from-square fa-xs"></i>';
            nameSpan.appendChild(ext);
        }

        li.appendChild(nameSpan);

        // Recursively add children (grandparents, etc.)
        if (parent.children && parent.children.length > 0) {
            const toggle = document.createElement("button");
            toggle.className = "lineage-toggle";
            toggle.innerHTML = '<i class="fa-solid fa-chevron-down fa-xs"></i>';
            toggle.title = opts.showAncestry || "";
            nameSpan.insertBefore(toggle, nameSpan.firstChild);

            const childTree = buildTree(parent.children, opts);
            childTree.className += " lineage-subtree";
            li.appendChild(childTree);

            toggle.addEventListener("click", () => {
                const isCollapsed = childTree.classList.toggle("collapsed");
                toggle.innerHTML = isCollapsed
                    ? '<i class="fa-solid fa-chevron-right fa-xs"></i>'
                    : '<i class="fa-solid fa-chevron-down fa-xs"></i>';
            });
        }

        ul.appendChild(li);
    });

    return ul;
}
