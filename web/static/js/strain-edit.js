document.addEventListener("DOMContentLoaded", () => {
    const editStrainForm = document.getElementById("editStrainForm");
    const deleteStrainButton = document.getElementById("deleteStrainButton");
    const editBreederSelect = document.getElementById("editBreederSelect");
    const editNewBreederName = document.getElementById("editNewBreederName");
    const editIndicaSativaSlider = document.getElementById("editIndicaSativaSlider");
    const editIndicaLabel = document.getElementById("editIndicaLabel");
    const editSativaLabel = document.getElementById("editSativaLabel");
    const editRatioFill = document.getElementById("editRatioFill");
    const descriptionTextarea = document.getElementById("editStrainDescription");
    const markdownPreview = document.getElementById("markdownPreview");

    const breederAc = IsleyAutocomplete.breederPicker(editBreederSelect, editNewBreederName);

    // "Flowering Time" for photoperiods, "Seed to Harvest" for autoflowers.
    const autoflowerSelect = document.getElementById("editAutoflower");
    if (autoflowerSelect) {
        autoflowerSelect.addEventListener("change", () => {
            const mode = autoflowerSelect.value === "true" ? "auto" : "photo";
            ["editCycleTimeLabel", "editCycleTimeDesc"].forEach(id => {
                const el = document.getElementById(id);
                if (el) el.textContent = el.dataset[mode];
            });
        });
    }

    // --- Seed pack image (saved immediately, independent of the form) ---
    const packagingCard = document.getElementById("packagingCard");
    if (packagingCard) {
        const base = `/strains/${packagingCard.dataset.strainId}/packaging-image`;
        const preview = document.getElementById("packagingPreview");
        const uploadBtn = document.getElementById("packagingUploadBtn");
        const removeBtn = document.getElementById("packagingRemoveBtn");
        const fileInput = document.getElementById("packagingFile");

        const render = (url) => {
            preview.classList.toggle("d-none", !url);
            if (url) preview.src = url + "?t=" + Date.now(); else preview.removeAttribute("src");
            removeBtn.classList.toggle("d-none", !url);
            uploadBtn.textContent = url ? uploadBtn.dataset.replace : uploadBtn.dataset.upload;
        };

        const send = async (method, path, body) => {
            const resp = await fetch(base + path, { method, body });
            const data = await resp.json().catch(() => ({}));
            if (typeof uiMessages !== "undefined") uiMessages.showToast(data.message || data.error || "", resp.ok ? "success" : "danger");
            if (resp.ok) {
                render(data.packaging_image);
                // Accept, discard and upload all resolve a waiting CannaDB image.
                const pending = document.getElementById("packagingPending");
                if (pending && method === "POST") pending.remove();
            }
        };

        uploadBtn.addEventListener("click", () => fileInput.click());
        fileInput.addEventListener("change", () => {
            const file = fileInput.files[0];
            if (!file) return;
            const form = new FormData();
            form.append("image", file);
            send("POST", "", form);
            fileInput.value = "";
        });
        removeBtn.addEventListener("click", () => send("DELETE", ""));
        packagingCard.querySelectorAll("[data-packaging-action]").forEach(btn => {
            btn.addEventListener("click", () => send("POST", "/" + btn.dataset.packagingAction));
        });
    }

    // --- Cannabinoids + effects/flavors/terpenes/medical-uses lists ---
    const profileCard = document.getElementById("strainProfileCard");
    const removeLabel = profileCard ? profileCard.dataset.remove : "";

    function addChip(editor, raw) {
        const name = raw.trim();
        if (!name) return;
        const chips = editor.querySelector(".strain-attr-chips");
        const exists = [...chips.querySelectorAll(".strain-attr-chip")]
            .some(c => c.dataset.name.toLowerCase() === name.toLowerCase());
        if (exists) return;
        const chip = document.createElement("span");
        chip.className = "badge text-bg-secondary strain-attr-chip";
        chip.dataset.name = name;
        chip.textContent = name;
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "btn-close btn-close-white ms-1 strain-attr-remove";
        btn.style.fontSize = ".55em";
        btn.setAttribute("aria-label", removeLabel);
        chip.appendChild(btn);
        chips.appendChild(chip);
    }

    document.querySelectorAll(".strain-attr-editor").forEach(editor => {
        const input = editor.querySelector(".strain-attr-input");
        editor.querySelectorAll(".strain-attr-remove").forEach(b => b.setAttribute("aria-label", removeLabel));
        input.addEventListener("keydown", (e) => {
            if (e.key === "Enter" || e.key === ",") {
                e.preventDefault();
                addChip(editor, input.value);
                input.value = "";
            }
        });
        // Text left in the box when the user clicks away (or on Save) still counts.
        input.addEventListener("blur", () => {
            addChip(editor, input.value);
            input.value = "";
        });
    });

    if (profileCard) {
        profileCard.addEventListener("click", (e) => {
            const btn = e.target.closest(".strain-attr-remove");
            if (btn) btn.closest(".strain-attr-chip").remove();
        });
    }

    function collectProfile() {
        const num = (id) => {
            const v = document.getElementById(id).value.trim();
            return v === "" ? null : parseFloat(v);
        };
        document.querySelectorAll(".strain-attr-editor").forEach(editor => {
            const input = editor.querySelector(".strain-attr-input");
            addChip(editor, input.value);
            input.value = "";
        });
        const attributes = {};
        document.querySelectorAll(".strain-attr-editor").forEach(editor => {
            attributes[editor.dataset.group] = [...editor.querySelectorAll(".strain-attr-chip")].map(c => {
                const a = { name: c.dataset.name };
                if (c.dataset.intensity) a.intensity = parseFloat(c.dataset.intensity);
                if (c.dataset.level) a.level = c.dataset.level;
                return a;
            });
        });
        return {
            cannabinoids: {
                thc_min: num("editThcMin"), thc_max: num("editThcMax"),
                cbd_min: num("editCbdMin"), cbd_max: num("editCbdMax"),
                cbn_max: num("editCbnMax"), cbg_max: num("editCbgMax"),
            },
            attributes,
        };
    }

    // --- Indica/Sativa slider with live ratio bar preview ---
    if (editIndicaSativaSlider) {
        editIndicaSativaSlider.addEventListener("input", () => {
            const indica = editIndicaSativaSlider.value;
            const sativa = 100 - indica;
            if (editIndicaLabel) editIndicaLabel.textContent = `Indica: ${indica}%`;
            if (editSativaLabel) editSativaLabel.textContent = `Sativa: ${sativa}%`;
            if (editRatioFill) editRatioFill.style.width = `${indica}%`;
        });
    }

    // --- Markdown live preview (debounced) ---
    let previewTimeout = null;
    if (descriptionTextarea && markdownPreview) {
        descriptionTextarea.addEventListener("input", () => {
            clearTimeout(previewTimeout);
            previewTimeout = setTimeout(() => {
                renderMarkdownPreview(descriptionTextarea.value);
            }, 300);
        });
    }

    function renderMarkdownPreview(md) {
        // Client-side markdown rendering using a simple approach
        // We'll use the server's markdownify endpoint or do basic client-side rendering
        // For now, do a lightweight client-side render
        let html = md;

        // Escape HTML first
        html = html.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

        // Headers
        html = html.replace(/^######\s+(.+)$/gm, '<h6>$1</h6>');
        html = html.replace(/^#####\s+(.+)$/gm, '<h5>$1</h5>');
        html = html.replace(/^####\s+(.+)$/gm, '<h4>$1</h4>');
        html = html.replace(/^###\s+(.+)$/gm, '<h3>$1</h3>');
        html = html.replace(/^##\s+(.+)$/gm, '<h2>$1</h2>');
        html = html.replace(/^#\s+(.+)$/gm, '<h1>$1</h1>');

        // Bold and italic
        html = html.replace(/\*\*\*(.+?)\*\*\*/g, '<strong><em>$1</em></strong>');
        html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
        html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

        // Links
        html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank">$1</a>');

        // Images
        html = html.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, '<img src="$2" alt="$1" style="max-width:100%">');

        // Inline code
        html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

        // Unordered lists
        html = html.replace(/^[-*]\s+(.+)$/gm, '<li>$1</li>');
        html = html.replace(/(<li>.*<\/li>\n?)+/g, '<ul>$&</ul>');

        // Horizontal rules
        html = html.replace(/^---+$/gm, '<hr>');

        // Paragraphs (double newlines)
        html = html.replace(/\n\n/g, '</p><p>');
        html = '<p>' + html + '</p>';

        // Clean up empty paragraphs
        html = html.replace(/<p>\s*<\/p>/g, '');
        html = html.replace(/<p>\s*(<h[1-6]>)/g, '$1');
        html = html.replace(/(<\/h[1-6]>)\s*<\/p>/g, '$1');
        html = html.replace(/<p>\s*(<ul>)/g, '$1');
        html = html.replace(/(<\/ul>)\s*<\/p>/g, '$1');
        html = html.replace(/<p>\s*(<hr>)/g, '$1');
        html = html.replace(/(<hr>)\s*<\/p>/g, '$1');

        markdownPreview.innerHTML = html;
    }

    // --- Form submission ---
    if (editStrainForm) {
        editStrainForm.addEventListener("submit", (e) => {
            e.preventDefault();
            if (breederAc) breederAc.commit();
            if (!editBreederSelect.value) {
                if (breederAc) breederAc.input.reportValidity();
                return;
            }

            const strainId = document.getElementById("editStrainId").value;
            const payload = {
                name: document.getElementById("editStrainName").value,
                breeder_id: editBreederSelect.value === "new" ? null : parseInt(editBreederSelect.value, 10),
                new_breeder: editBreederSelect.value === "new" ? editNewBreederName.value : null,
                indica: parseInt(editIndicaSativaSlider.value, 10),
                sativa: 100 - parseInt(editIndicaSativaSlider.value, 10),
                autoflower: document.getElementById("editAutoflower").value === "true",
                seed_count: parseInt(document.getElementById("editSeedCount").value, 10),
                seed_location: document.getElementById("editSeedLocation").value,
                description: descriptionTextarea.value,
                short_desc: document.getElementById("editStrainShortDescription").value,
                cycle_time: parseInt(document.getElementById("editCycleTime").value, 10),
                url: document.getElementById("editUrl").value,
                growing: {
                    height_indoor: document.getElementById("editHeightIndoor").value,
                    height_outdoor: document.getElementById("editHeightOutdoor").value,
                    yield_indoor: document.getElementById("editYieldIndoor").value,
                    yield_outdoor: document.getElementById("editYieldOutdoor").value,
                },
            };
            if (profileCard) Object.assign(payload, collectProfile());

            fetch(`/strains/${strainId}`, {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(payload),
            })
                .then(async response => {
                    if (!response.ok) {
                        let serverMsg = "";
                        try { const data = await response.json(); serverMsg = (data && data.error) || ""; } catch (e) {}
                        const err = new Error(serverMsg || "Failed to update strain");
                        err.serverMessage = serverMsg;
                        throw err;
                    }
                    // Save lineage if the editor is present
                    if (typeof window.saveLineage === "function") {
                        return window.saveLineage().then(() => strainId);
                    }
                    return strainId;
                })
                .then(id => {
                    // Redirect back to the strain detail page
                    if (id && /^\d+$/.test(String(id))) {
                        window.location.href = `/strain/${id}`;
                    }
                })
                .catch(error => {
                    console.error("Error updating strain:", error);
                    if (typeof uiMessages !== 'undefined') {
                        const msg = (error && error.serverMessage) || uiMessages.t('update_error') || 'Update failed';
                        uiMessages.showToast(msg, 'danger');
                    }
                });
        });
    }

    // --- Delete ---
    if (deleteStrainButton) {
        deleteStrainButton.addEventListener("click", () => {
            const strainId = document.getElementById("editStrainId").value;

            const doDelete = () => {
                fetch(`/strains/${strainId}`, { method: "DELETE" })
                    .then(response => {
                        if (!response.ok) throw new Error("Failed to delete strain");
                        window.location.href = "/strains";
                    })
                    .catch(error => {
                        console.error("Error deleting strain:", error);
                        if (typeof uiMessages !== 'undefined') {
                            uiMessages.showToast(uiMessages.t('delete_error') || 'Delete failed', 'danger');
                        }
                    });
            };

            if (typeof uiMessages !== 'undefined' && uiMessages.showConfirm) {
                uiMessages.showConfirm(uiMessages.t('confirm_delete_strain') || 'Are you sure you want to delete this strain?').then(confirmed => {
                    if (confirmed) doDelete();
                });
            } else if (confirm('Are you sure you want to delete this strain?')) {
                doDelete();
            }
        });
    }
});

// --- Markdown toolbar helpers ---
function mdWrap(before, after) {
    const ta = document.getElementById("editStrainDescription");
    if (!ta) return;
    const start = ta.selectionStart;
    const end = ta.selectionEnd;
    const selected = ta.value.substring(start, end);
    const replacement = before + (selected || 'text') + after;
    ta.setRangeText(replacement, start, end, 'select');
    ta.focus();
    ta.dispatchEvent(new Event('input'));
}

function mdPrefix(prefix) {
    const ta = document.getElementById("editStrainDescription");
    if (!ta) return;
    const start = ta.selectionStart;
    const end = ta.selectionEnd;
    const selected = ta.value.substring(start, end);
    const lines = selected ? selected.split('\n') : [''];
    const replacement = lines.map(l => prefix + l).join('\n');
    ta.setRangeText(replacement, start, end, 'select');
    ta.focus();
    ta.dispatchEvent(new Event('input'));
}

function mdLink() {
    const ta = document.getElementById("editStrainDescription");
    if (!ta) return;
    const start = ta.selectionStart;
    const end = ta.selectionEnd;
    const selected = ta.value.substring(start, end);
    const replacement = `[${selected || 'link text'}](url)`;
    ta.setRangeText(replacement, start, end, 'select');
    ta.focus();
    ta.dispatchEvent(new Event('input'));
}

// Attach markdown toolbar buttons via event delegation (replaces inline onclick)
document.addEventListener("click", (e) => {
    const btn = e.target.closest(".md-btn");
    if (!btn) return;
    const action = btn.dataset.mdAction;
    if (action === "wrap") mdWrap(btn.dataset.mdBefore, btn.dataset.mdAfter);
    else if (action === "prefix") mdPrefix(btn.dataset.mdPrefix);
    else if (action === "link") mdLink();
});
