/**
 * Strain Lineage Editor
 * Each parent row is a type-ahead over the user's strains. Picking one links
 * the parent (blue link on the strain page); any other typed name is saved
 * unlinked (red link).
 */
document.addEventListener("DOMContentLoaded", () => {
    const editor = document.getElementById("lineageEditor");
    const entriesContainer = document.getElementById("lineageEntries");
    const addParentBtn = document.getElementById("addParentBtn");

    if (!editor || !entriesContainer || !addParentBtn || typeof currentStrainID === "undefined") return;

    const lbl = editor.dataset;
    const rows = []; // { row, select, newName, ac }
    let allStrains = [];

    const ready = Promise.all([
        fetch("/strains/in-stock", { cache: "no-store" }).then(r => r.json()).catch(() => []),
        fetch("/strains/out-of-stock", { cache: "no-store" }).then(r => r.json()).catch(() => []),
        fetch(`/strains/${currentStrainID}/lineage`, { cache: "no-store" }).then(r => r.json()).catch(() => []),
    ]).then(([inStock, outOfStock, lineage]) => {
        allStrains = [...(inStock || []), ...(outOfStock || [])]
            .filter(s => s.id !== currentStrainID)
            .sort((a, b) => a.name.localeCompare(b.name));
        (lineage || []).forEach(entry => addParentRow(entry.parent_name, entry.parent_strain_id));
    });

    addParentBtn.addEventListener("click", () => {
        ready.then(() => {
            const r = addParentRow("", null);
            r.ac.input.focus();
        });
    });

    // Called by strain-edit.js after the strain itself saves.
    window.saveLineage = function () {
        return fetch(`/strains/${currentStrainID}/lineage`, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ parents: collectParents() }),
        });
    };

    function collectParents() {
        const parents = [];
        rows.forEach(({ row, select, newName, ac }) => {
            if (!row.isConnected) return;
            ac.commit();
            const val = select.value;
            if (val === "new") {
                const name = newName.value.trim();
                if (name) parents.push({ parent_name: name, parent_strain_id: null });
            } else if (val) {
                const opt = select.querySelector(`option[value="${CSS.escape(val)}"]`);
                parents.push({ parent_name: opt ? opt.textContent.trim() : "", parent_strain_id: parseInt(val, 10) });
            }
        });
        return parents.filter(p => p.parent_name);
    }

    function addParentRow(name, strainId) {
        const row = document.createElement("div");
        row.className = "lineage-entry-row d-flex align-items-center gap-2 mb-2";

        const inputWrapper = document.createElement("div");
        inputWrapper.className = "flex-grow-1";

        const select = document.createElement("select");
        select.className = "form-select form-select-sm";
        select.appendChild(new Option("", ""));
        allStrains.forEach(s => {
            const opt = new Option(s.name, s.id);
            opt.dataset.breeder = s.breeder || "";
            select.appendChild(opt);
        });
        select.appendChild(new Option("", "new"));

        const newName = document.createElement("input");
        newName.type = "hidden";

        inputWrapper.appendChild(select);
        inputWrapper.appendChild(newName);

        const matchBadge = document.createElement("span");
        matchBadge.className = "lineage-match-badge ms-2";

        const removeBtn = document.createElement("button");
        removeBtn.type = "button";
        removeBtn.className = "btn btn-sm btn-outline-danger";
        removeBtn.title = lbl.remove || "";
        removeBtn.setAttribute("aria-label", lbl.remove || "");
        removeBtn.innerHTML = '<i class="fa-solid fa-xmark"></i>';

        row.appendChild(inputWrapper);
        row.appendChild(matchBadge);
        row.appendChild(removeBtn);
        entriesContainer.appendChild(row);

        const ac = new IsleyAutocomplete(select, {
            placeholder: lbl.placeholder || "",
            newFromText: true,
            newNameInput: newName,
            sublabel: opt => opt.dataset.breeder || "",
            maxResults: 8,
            labels: { newText: lbl.useText || 'Use "{name}"', results: lbl.results || "{count} results available" },
        });
        ac.input.classList.add("form-control-sm", "lineage-parent-name");

        const entry = { row, select, newName, ac };
        rows.push(entry);

        if (strainId && select.querySelector(`option[value="${strainId}"]`)) {
            ac.setValue(String(strainId));
        } else if (name) {
            // Load saved names as they were; don't silently link on open.
            ac.setNewName(name, false);
        }

        const syncBadge = () => updateMatchBadge(matchBadge, select.value && select.value !== "new");
        select.addEventListener("change", syncBadge);
        syncBadge();

        removeBtn.addEventListener("click", () => {
            row.remove();
            const i = rows.indexOf(entry);
            if (i >= 0) rows.splice(i, 1);
        });

        return entry;
    }

    function updateMatchBadge(badge, linked) {
        if (linked) {
            badge.innerHTML = '<i class="fa-solid fa-link fa-xs"></i>';
            badge.className = "lineage-match-badge lineage-matched";
            badge.title = lbl.linked || "";
        } else {
            badge.innerHTML = '<i class="fa-solid fa-link-slash fa-xs"></i>';
            badge.className = "lineage-match-badge lineage-unmatched";
            badge.title = lbl.unlinked || "";
        }
    }
});
