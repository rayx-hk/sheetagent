# Task Type Strategy Hints

## find
Goal: Locate specific cells/rows matching criteria
Strategy: Value-Write — filter rows, write matching values to target
Key risk: Partial match vs exact match, case sensitivity

## extract
Goal: Pull data from one location to another
Strategy: Value-Write — read source, write to target
Key risk: Column offset errors, header mismatch

## sum / calculate
Goal: Compute aggregated values
Strategy: Formula-First — write =SUM, =SUMIF, =AVERAGE formulas
Key risk: Include/exclude header row, empty cell handling

## highlight / format
Goal: Change cell appearance (color, bold, borders)
Strategy: Style-Modification (Pattern SL-3) — modify cell.font, cell.fill, etc.
Key risk: Overwriting existing styles, merged cell regions

## remove / delete
Goal: Remove rows, columns, or specific data
Strategy: Careful deletion with reference preservation (Pattern SL-1)
Key risk: Shifting all references below/right, breaking existing formulas

## modify
Goal: Update existing cell values
Strategy: Value-Write — in-place modification
Key risk: Overwriting wrong cells, type coercion

## count
Goal: Count cells matching criteria
Strategy: Formula-First — write =COUNTIF, =COUNTIFS
Key risk: Empty cell counting, criteria interpretation

## display
Goal: Arrange/format data for presentation
Strategy: Mixed — may involve value rearrangement + formatting
Key risk: Multi-level headers, merged cell layout expectations