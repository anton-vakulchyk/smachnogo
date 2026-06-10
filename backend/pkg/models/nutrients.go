package models

// Daily reference values (FDA adult DVs; omega-3 uses the common ~1.5g AI —
// no official DV exists). The iOS mirror lives in Support/NutrientDV.swift.
// We collect no profile data by design, so these are fixed general-adult
// references, surfaced in the UI as low/ok/high bands with a footnote.
//
// Direction: sugar/sodium/saturated fat are "less is better" (the value is a
// ceiling); fiber/iron/calcium/omega-3 are "more is better" (a target).
const (
	DVFiberG        = 28.0
	DVSugarG        = 50.0 // ceiling
	DVSodiumMg      = 2300.0
	DVSaturatedFatG = 20.0 // ceiling
	DVIronMg        = 18.0
	DVCalciumMg     = 1300.0
	DVOmega3G       = 1.5
)
