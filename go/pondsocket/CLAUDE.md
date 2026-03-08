# 🔐 AI EXECUTION CONTEXT — ZERO-TOLERANCE MODE

> This context file defines **absolute behavioral rules** for any AI model operating in this codebase.
> **You do not assume. You do not guess. You do not proceed unless explicitly told.**

---

## 📁 1. FILE & DIRECTORY ACCESS POLICY

### 🚫 Forbidden:

* Under **no circumstances** may you access, read, scan, index, or mention any file or directory containing `old/` in its path or name.
* You must act as though the `old/` folder **does not exist** unless the user explicitly says the phrase:

> **ACCESS THE OLD FOLDER NOW**

### ✅ Allowed:

* Only access folders and files that are:

    * Mentioned explicitly in the current instruction;
    * OR part of a previously approved and active implementation plan.

---

## 🧭 2. PLAN-FIRST ENFORCEMENT (NO AUTONOMY)

### 🚫 Forbidden:

* You may **not** begin implementing, modifying, generating, or deleting **any code or file** until the following conditions are met:

1. A complete, structured implementation plan has been written by you and approved by the user.
2. The user responds with an explicit go-ahead such as:

   > **PLAN APPROVED — PROCEED**

### ⚠️ Clarification:

* Do not infer approval from context. If you're unsure, **STOP** and ask for explicit validation.

---

## 🌿 3. GIT OPERATIONS — LOCKED BY DEFAULT

### 🚫 Forbidden:

* You must treat all Git commands and operations as **disabled and unavailable**.

* This includes (but is not limited to):
  `git init`, `git clone`, `git add`, `git commit`, `git push`, `git status`, `git stash`, etc.

### ✅ Allowed Only If:

* You are explicitly instructed with a phrase such as:

  > **ENABLE GIT OPERATIONS NOW**

* No synonyms or inferred permission are valid.

---

## 🧼 4. CODE COMMENTS — HARD BAN POLICY

### 🚫 ABSOLUTELY FORBIDDEN:

* You must **never** write a comment in code **unless it is a strict `TODO:`**.
* All the following are **prohibited**:

    * Explanatory comments
    * Docstrings
    * Style markers
    * Notes about logic or reasoning
    * Debug labels
    * Non-actionable or vague `TODO`s

### ✅ ONLY ALLOWED:

* A `TODO:` comment is allowed **only when**:

    1. The code block is incomplete, AND
    2. The `TODO:` explains exactly what needs to be implemented, AND
    3. The function is structurally necessary (e.g., part of an interface or dependency)

### ✅ Example (ACCEPTED):

```python
def generate_invoice_pdf(order):
    # TODO: Fetch customer details and render PDF using ReportLab
    pass
```

### ❌ Examples (REJECTED):

```python
# Handles PDF generation  ❌ Unnecessary
# TODO: implement  ❌ Too vague
# render here  ❌ Not actionable
```

### Final Rule:

> If there’s no actionable `TODO:`, the function must remain **100% comment-free**.
> Empty functions **must not** include even `pass` with a comment unless absolutely required by syntax.

---

## 🧩 5. STUBS & TODOs — TRACEABLE INTENT ONLY

### ✅ Required:

* Every stub (placeholder function/class/module) must have:

    * A valid, specific `TODO:` as defined above
    * A structural reason to exist

### 🚫 Forbidden:

* No generic placeholders like:

    * `# TODO: implement this`
    * `def something(): pass`
* No speculative scaffolding. Only create stubs **with a clear, intended implementation.**

---

## 🧪 6. TESTING AND BUILDING — MANDATORY ON COMPLETION

### ✅ Required:

* After completing any code change, you **must**:

    1. Run the associated test suite;
    2. Validate that all tests pass;
    3. Build the relevant module or package (if applicable);
    4. Report any test failures or build issues instead of proceeding.

### 🚫 Forbidden:

* Do **not** assume the code works without verification.
* Do **not** skip testing due to time, simplicity, or logic confidence.
* There are **no exceptions**.

---

## 📦 7. REUSE OVER REINVENTION — PACKAGE-PRIORITY MANDATE

### ✅ Required:

* You **must prefer** a battle-tested, open-source package over writing custom implementations **whenever**:

    * The feature is common or standardized (e.g., validation, date formatting, API requests);
    * The package is:

        * Maintained (recent commits),
        * Stable (not beta),
        * Compatible (license and language match)

* ✅ If in doubt, ask:

  > "Can we use X instead of implementing this?"

### 🚫 Forbidden:

* You **must not** reimplement what is already solved by an existing, high-quality package unless:

    * Explicit permission is given to do so;
    * OR the package has critical shortcomings that are documented and confirmed.

---

## ⛔ 8. DEFAULT MODE: INACTIVE

> If you are not told to act, your status is:

```
AI STATUS: WAITING FOR INSTRUCTION
```

### ✅ You may:

* Plan
* Ask for clarification
* Wait

### 🚫 You may NOT:

* Infer intent
* Generate code
* Alter files
* Begin tasks
* Comment on logic or architecture **unless asked**
