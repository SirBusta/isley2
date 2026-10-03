// Stock fields on Add/Edit Strain (and the import review page): "Wanted" and
// "Seeds added on" follow the seed count, matching the server's rules —
// seeds in stock can't be wanted, and with no seeds there's nothing to date.
// Stocking from zero fills in today's date (still editable).
window.strainStockFields = function () {
    const count = document.getElementById("editSeedCount");
    const added = document.getElementById("editSeedsAddedOn");
    const wanted = document.getElementById("editWanted");
    if (!count || !added || !wanted) return null;

    const today = () => {
        const d = new Date();
        const pad = (n) => String(n).padStart(2, "0");
        return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
    };
    const seeds = () => parseInt(count.value, 10) || 0;
    let lastSeeds = seeds();

    function sync() {
        const inStock = seeds() > 0;
        if (inStock && lastSeeds <= 0 && !added.value) added.value = today();
        if (inStock) wanted.checked = false;
        wanted.disabled = inStock;
        added.disabled = !inStock;
        lastSeeds = seeds();
    }
    count.addEventListener("input", sync);
    sync();

    return {
        collect: () => ({
            wanted: wanted.checked && seeds() <= 0,
            seeds_added_on: seeds() > 0 ? added.value : "",
        }),
    };
};
