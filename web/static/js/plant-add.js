document.addEventListener("DOMContentLoaded", () => {
    const addPlantForm = document.getElementById("addPlantForm");
    const zoneSelect = document.getElementById("zoneSelect");
    const newZoneInput = document.getElementById("newZoneInput");
    const strainSelect = document.getElementById("strainSelect");
    const newStrainCard = document.getElementById("newStrainCard");
    const breederSelect = document.getElementById("breederSelect");
    const newBreederInput = document.getElementById("newBreederInput");
    const parentPlantDropdown = document.getElementById("parentPlantDropdown");
    const parentPlantSelect = document.getElementById("parentPlantSelect");
    const isClone = document.getElementById("isClone");
    const plantPreview = document.getElementById("plantPreview");

    // Initialize autocomplete on strain and zone selects
    const zoneAC = new IsleyAutocomplete(zoneSelect, {
        placeholder: "Type to search zones...",
    });
    const strainAC = new IsleyAutocomplete(strainSelect, {
        placeholder: "Type to search strains...",
    });

    // Show/Hide New Zone Input
    zoneSelect.addEventListener("change", () => {
        newZoneInput.classList.toggle("d-none", zoneSelect.value !== "new");
    });

    // Show/Hide New Breeder Input to match breederSelect's current value.
    // Called both on breederSelect's own change AND whenever the New
    // Strain card is revealed, because when there are zero breeders yet
    // "Add New Breeder" is the sole/default <option> — the select is
    // already on "new" without the user ever changing it, so a
    // change-only listener never fires and the input stayed hidden.
    function syncNewBreederInput() {
        newBreederInput.classList.toggle("d-none", breederSelect.value !== "new");
    }

    // Show/Hide New Strain Card
    strainSelect.addEventListener("change", () => {
        newStrainCard.classList.toggle("d-none", strainSelect.value !== "new");
        if (strainSelect.value === "new") {
            syncNewBreederInput();
        }

        // Load parent plants for selected strain
        if (strainSelect.value !== "new" && strainSelect.value) {
            fetch(`/plants/by-strain/${strainSelect.value}`)
                .then(r => r.json())
                .then(plants => {
                    parentPlantSelect.innerHTML = '<option value="0">None</option>';
                    plants.forEach(p => {
                        const opt = document.createElement("option");
                        opt.value = p.id;
                        opt.textContent = p.name;
                        parentPlantSelect.appendChild(opt);
                    });
                })
                .catch(() => {});
        }
        updatePreview();
    });

    // Show/Hide New Breeder Input
    breederSelect.addEventListener("change", () => {
        syncNewBreederInput();
    });

    // Show/Hide Parent Plant Dropdown
    isClone.addEventListener("change", () => {
        parentPlantDropdown.classList.toggle("d-none", !isClone.checked);
    });

    // Live preview update
    function updatePreview() {
        const name = document.getElementById("plantName").value || "Untitled Plant";
        const zone = zoneSelect.value === "new"
            ? (document.getElementById("newZoneName").value || "New Zone")
            : (zoneAC.input ? zoneAC.input.value : "—") || "—";
        const strain = strainSelect.value === "new"
            ? (document.getElementById("newStrainName").value || "New Strain")
            : (strainAC.input ? strainAC.input.value : "—") || "—";

        plantPreview.innerHTML = `
            <div class="text-start">
                <h4 class="text-primary mb-2">${esc(name)}</h4>
                <ul class="list-unstyled strain-info-list">
                    <li>
                        <span class="strain-info-label"><i class="fa-solid fa-cannabis me-2"></i>Strain</span>
                        <span class="strain-info-value">${esc(strain)}</span>
                    </li>
                    <li>
                        <span class="strain-info-label"><i class="fa-solid fa-location-dot me-2"></i>Zone</span>
                        <span class="strain-info-value">${esc(zone)}</span>
                    </li>
                    <li>
                        <span class="strain-info-label"><i class="fa-solid fa-clone me-2"></i>Clone</span>
                        <span class="strain-info-value">${isClone.checked ? "Yes" : "No"}</span>
                    </li>
                </ul>
            </div>
        `;
    }

    function esc(str) {
        if (!str) return "";
        const d = document.createElement("div");
        d.textContent = str;
        return d.innerHTML;
    }

    // Attach preview updates to form inputs
    document.getElementById("plantName").addEventListener("input", updatePreview);
    zoneSelect.addEventListener("change", updatePreview);
    strainSelect.addEventListener("change", updatePreview);
    isClone.addEventListener("change", updatePreview);
    document.getElementById("newZoneName")?.addEventListener("input", updatePreview);
    document.getElementById("newStrainName")?.addEventListener("input", updatePreview);

    // Form submission
    addPlantForm.addEventListener("submit", (e) => {
        e.preventDefault();

        formHelpers.clearFieldError(zoneAC.input);
        formHelpers.clearFieldError(strainAC.input);

        // The autocomplete only sets zoneSelect/strainSelect's value when an
        // existing item or "Add New" is explicitly chosen from its dropdown.
        // Typing a name and clicking away without picking anything leaves it
        // blank, which previously reached the server as neither an existing
        // ID nor new-item data and crashed the handler.
        let hasError = false;
        if (zoneSelect.value === "") {
            formHelpers.setFieldError(zoneAC.input, uiMessages.t('api_zone_required'));
            hasError = true;
        }
        if (strainSelect.value === "") {
            formHelpers.setFieldError(strainAC.input, uiMessages.t('api_strain_required'));
            hasError = true;
        }
        if (hasError) return;

        const submitBtn = addPlantForm.querySelector('button[type="submit"]');
        submitBtn.classList.add("is-loading");
        submitBtn.disabled = true;

        const payload = {
            name: document.getElementById("plantName").value,
            zone_id: zoneSelect.value === "new" ? null : parseInt(zoneSelect.value, 10),
            new_zone: zoneSelect.value === "new" ? document.getElementById("newZoneName").value : null,
            strain_id: strainSelect.value === "new" ? null : parseInt(strainSelect.value, 10),
            new_strain: strainSelect.value === "new" ? {
                name: document.getElementById("newStrainName").value,
                breeder_id: breederSelect.value === "new" ? null : parseInt(breederSelect.value, 10),
                new_breeder: breederSelect.value === "new" ? document.getElementById("newBreederName").value : null
            } : null,
            status_id: parseInt(document.getElementById("statusSelect").value, 10),
            date: document.getElementById("startDate").value,
            clone: isClone.checked ? 1 : 0,
            parent_id: parseInt(parentPlantSelect.value, 10),
            decrement_seed_count: document.getElementById("decrementSeedCount").checked,
        };

        fetch("/plants", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
        })
        .then(response => {
            if (!response.ok) throw new Error("Failed to add plant");
            return response.json();
        })
        .then(data => {
            // Redirect to the new plant's detail page if possible
            if (data && data.id) {
                window.location.href = `/plant/${data.id}`;
            } else {
                window.location.href = "/plants";
            }
        })
        .catch(error => {
            submitBtn.classList.remove("is-loading");
            submitBtn.disabled = false;
            uiMessages.showToast(uiMessages.t('failed_to_add_plant') || 'Failed to add plant. Try again.', 'danger');
        });
    });
});
