# Graph Report - .  (2026-08-25)

## Corpus Check
- Corpus is ~26,399 words - fits in a single context window. You may not need a graph.

## Summary
- 298 nodes · 313 edges · 28 communities (21 shown, 7 thin omitted)
- Extraction: 95% EXTRACTED · 5% INFERRED · 0% AMBIGUOUS · INFERRED: 15 edges (avg confidence: 0.9)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- Release Reviews and Assurance
- Project Rules and Build
- Scheduling Data Model
- Product Architecture Decisions
- Security and Transaction Controls
- Test Strategy and Repository
- Production Hardening Architecture
- Security Privacy and Configuration
- Controlled Voice Workflow
- Definition of Done and Backlog
- Operations Deployment and Recovery
- Core Data and Availability
- Dashboard Mobile and Calendar Feeds
- Technical Source References
- V1 Product Scope
- Domain State Machines
- Application Ports and Adapters
- Calendar Interaction Design
- Release Acceptance Traceability
- Concurrency Invariants
- Task Planning and Review
- Performance and Release Gates
- Accessible Error Interaction
- Responsive UX Test Matrix
- Disaster Recovery and Failure Tests
- Server Rendered Hybrid UI
- UTC and DST Correctness
- Go Module Identity

## God Nodes (most connected - your core abstractions)
1. `HackPlan Project` - 10 edges
2. `Technische Quellenbasis` - 10 edges
3. `Sicherheits- und Datenschutzanforderungen` - 9 edges
4. `Betrieb und Deployment` - 8 edges
5. `Entscheidungen und Annahmen` - 8 edges
6. `Production Hardening Task` - 7 edges
7. `Security Privacy and Abuse Review` - 7 edges
8. `Appointments` - 7 edges
9. `Produkt-Brainstorming und Erweiterungslandkarte` - 7 edges
10. `Transactional Conflict-Safe Scheduling` - 6 edges

## Surprising Connections (you probably didn't know these)
- `Mandatory Core Architecture Rules` --semantically_similar_to--> `Secure Responsive Modular Go Monolith`  [INFERRED] [semantically similar]
  codex/MASTER_PROMPT.md → AGENTS.md
- `HackPlan Product Decisions` --semantically_similar_to--> `Admin-Only Appointment Scheduling`  [INFERRED] [semantically similar]
  README.md → AGENTS.md
- `HackPlan Review Skill` --references--> `HackPlan Project`  [EXTRACTED]
  .agents/skills/hackplan-review/SKILL.md → AGENTS.md
- `Revocable Hashed Calendar Feed Secret Model` --conceptually_related_to--> `Authentication and Token Security`  [EXTRACTED]
  codex/tasks/07-ics-calendar-feed.md → AGENTS.md
- `ExecPlan Risks Tests Progress and Evidence` --conceptually_related_to--> `Quality Gates and Definition of Done`  [INFERRED]
  PLANS.md → AGENTS.md

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Atomic Appointment Fixation Flow** — codex_tasks_04_calendar_scheduling_reservation_model, codex_tasks_04_calendar_scheduling_scheduling_use_cases, agents_transactional_outbox [EXTRACTED 1.00]
- **Reliable Customer Notification Flow** — codex_tasks_05_notifications_confirmation_outbox_confirmation_model, codex_tasks_05_notifications_confirmation_delivery_worker_providers, codex_tasks_05_notifications_confirmation_public_confirmation_security [EXTRACTED 1.00]
- **Explainable Proposal Pipeline** — codex_tasks_08_planning_suggestions_routing_gates_and_candidates, codex_tasks_08_planning_suggestions_routing_scoring_and_routing, codex_tasks_08_planning_suggestions_routing_proposal_revalidation [EXTRACTED 1.00]
- **Release Candidate Assurance Flow** — codex_tasks_11_e2e_release_candidate_user_journey_suite, codex_tasks_11_e2e_release_candidate_acceptance_matrix, codex_tasks_11_e2e_release_candidate_security_reviews_gate, codex_tasks_11_e2e_release_candidate_performance_smoke, codex_tasks_11_e2e_release_candidate_release_gate [EXTRACTED 1.00]
- **Appointment Atomic Consistency** — docs_03_domain_model_appointments, docs_03_domain_model_appointment_drivers, docs_03_domain_model_appointment_resources, docs_03_domain_model_outbox_events, docs_03_domain_model_audit_events [EXTRACTED 1.00]
- **Controlled Voice Intake Flow** — docs_09_voice_intake_browser_recording, docs_09_voice_intake_transcriber_architecture, docs_09_voice_intake_extractor_architecture, docs_09_voice_intake_voice_draft_schema, docs_09_voice_intake_review_ui, docs_09_voice_intake_atomic_voice_commit [EXTRACTED 1.00]
- **Release Quality Gates** — docs_15_definition_of_done_task_quality_gate, docs_15_definition_of_done_ui_quality_gate, docs_15_definition_of_done_database_change_gate, docs_15_definition_of_done_provider_side_effect_gate, docs_15_definition_of_done_release_1_0_gate [EXTRACTED 1.00]
- **V1 Architecture Defaults** — docs_adr_0001_modular_monolith_modular_monolith, docs_adr_0002_postgresql_no_redis_postgresql_only_persistence, docs_adr_0003_server_rendered_hybrid_ui_hybrid_ui, docs_adr_0004_transactional_outbox_transactional_outbox, docs_adr_0005_rule_based_planning_explainable_rule_based_planning [EXTRACTED 1.00]
- **Scheduling Reliability Test Flow** — docs_11_test_strategy_postgresql_integration_tests, docs_11_test_strategy_concurrency_tests, docs_11_test_strategy_playwright_e2e, docs_11_test_strategy_development_seed_fixture [EXTRACTED 1.00]

## Communities (28 total, 7 thin omitted)

### Community 0 - "Release Reviews and Assurance"
Cohesion: 0.07
Nodes (31): Controlled Voice Intake Task, Voice Privacy Boundary, Security Reviews 90 to 93 Gate, Evidence-Based Abuse Case Suite, Browser and Public Token Review, Database Secrets Logs and Container Review, Provider SSRF and Voice Upload Review, Security Privacy and Abuse Review (+23 more)

### Community 1 - "Project Rules and Build"
Cohesion: 0.09
Nodes (30): Admin-Only Appointment Scheduling, HackPlan Project, Layered Dependency Direction, Secure Responsive Modular Go Monolith, Quality Gates and Definition of Done, HackPlan Implementation Skill, Vertical Slice Implementation Workflow, HackPlan Release Skill (+22 more)

### Community 2 - "Scheduling Data Model"
Cohesion: 0.09
Nodes (23): Planning Proposal Revalidation, Appointment Driver Reservations, Appointment Resource Reservations, Appointments, Audit Events, Driver Availability Rules and Exceptions, Calendar Feeds, Confirmation Requests (+15 more)

### Community 3 - "Product Architecture Decisions"
Cohesion: 0.09
Nodes (23): Admin-only Terminfixierung, Entscheidungen und Annahmen, Deterministische regelbasierte Planung, Generisches Ressourcenmodell, PostgreSQL als System of Record, Serverseitig gerenderte Hybrid-UI, Spracheingabe nur als Entwurf, ADR 0002: PostgreSQL ohne Redis (+15 more)

### Community 4 - "Security and Transaction Controls"
Cohesion: 0.10
Nodes (21): Authentication and Token Security, Controlled Draft-Only Assistance, HackPlan Review Skill, Risk-Based Code Review, Transactional Outbox Delivery, Transactional Conflict-Safe Scheduling, Admin Management Audit and Negative Tests, Users Drivers Passwords and Server Sessions (+13 more)

### Community 5 - "Test Strategy and Repository"
Cohesion: 0.10
Nodes (20): Schutz vor Doppelbuchungen, Parallelitätstests, Development-Seed als Testfixture, Europe/Vienna DST-Tests, Browser-E2E mit Playwright, PostgreSQL-Integrationstests, Testpyramide, Teststrategie (+12 more)

### Community 6 - "Production Hardening Architecture"
Cohesion: 0.11
Nodes (19): Tested Backup and Restore, Liveness Readiness and Provider Degradation, Production Hardening Task, Redacted Observability, Supply Chain Release Policy, HTTP Trust Boundary Controls, Authentication Session and RBAC Review, Outbox and Confirmation Idempotency (+11 more)

### Community 7 - "Security Privacy and Configuration"
Cohesion: 0.13
Nodes (16): Capability Tokens, CSRF-Schutz, Datenaufbewahrung, Opaque serverseitige Sessions, Redigiertes Logging, Sicherheits- und Datenschutzanforderungen, Serverseitige RBAC, Dependency- und Supply-Chain-Sicherheit (+8 more)

### Community 8 - "Controlled Voice Workflow"
Cohesion: 0.16
Nodes (14): Voice Review and Commit UI, Speech-to-Text Port, Voice Draft Workflow, Customers, Jobs, Voice Drafts and Atomic Commit, Waitlist Entries, Voice Draft API Routes (+6 more)

### Community 9 - "Definition of Done and Backlog"
Cohesion: 0.14
Nodes (14): Transactional Outbox, Datenbankänderungs-Gate, Definition of Done, Provider- und Nebenwirkungs-Gate, Task-Qualitätsgate, UI-Qualitätsgate, Regel für neue Features, P1 Pilot-Erweiterungen (+6 more)

### Community 10 - "Operations Deployment and Recovery"
Cohesion: 0.18
Nodes (12): Admin-CLI, Backup und Restore, Liveness und Readiness, Unveränderliches Container-Image, Logs und Metriken, Betrieb und Deployment, Produktionshärtung, Zieltopologie (+4 more)

### Community 11 - "Core Data and Availability"
Cohesion: 0.18
Nodes (11): Separate Domain Objects, Generic Resource Model, UTC Storage and Europe Vienna Display, Atomic Capture Optimistic Concurrency and Validation, Separate Customer Job Note and Waitlist Domain, Task 02 Customers Orders and Waitlist, Searchable Sortable Responsive Waitlist Workflow, Recurring Rules Exceptions and Availability Resolver (+3 more)

### Community 12 - "Dashboard Mobile and Calendar Feeds"
Cohesion: 0.20
Nodes (11): All Drivers See All Appointments, Responsive Accessible German UI, Responsive Calendar Conflict and Revert Experience, Accessibility Responsive and Query Performance Assurance, Bounded Operational Dashboard Read Model, Role-Specific Responsive Application Experience, Task 06 Dashboard and Mobile Experience, Revocable Hashed Calendar Feed Secret Model (+3 more)

### Community 13 - "Technical Source References"
Cohesion: 0.20
Nodes (10): chi v5 Dokumentation, Codex AGENTS.md, Best Practices und Skills, FullCalendar Dragging Documentation, Go Dokumentation, Google Maps URLs, iCalendar RFC 5545, OpenAI Speech-to-Text Documentation, PostgreSQL Dokumentation und Temporal Constraints (+2 more)

### Community 14 - "V1 Product Scope"
Cohesion: 0.29
Nodes (7): Private Read-Only Calendar Feed Scope, Notification and Customer Confirmation Scope, V1 Exclusions and Future Extensions, Explainable Planning Suggestion Scope, Release 1.0 Scope, Waitlist Calendar Drivers and Resources Scope, Controlled Voice Intake Scope

### Community 15 - "Domain State Machines"
Cohesion: 0.33
Nodes (6): Appointment Lifecycle State Machine, Customer Confirmation State Machine, Stable Domain Events, Job Workflow State Machine, Notification State Machine, Separated Status and State Model

### Community 16 - "Application Ports and Adapters"
Cohesion: 0.40
Nodes (5): Application Layer Dependency Direction, HackPlan Domain Modules, Replaceable Integration Ports, Optional OpenAI Voice Provider, Signed SMS Webhook Adapter

### Community 17 - "Calendar Interaction Design"
Cohesion: 0.50
Nodes (4): Appointment Planning Dialog, Drag Creates Draft Flow, Mobile Scheduling Alternative, Waitlist and Calendar UI

### Community 18 - "Release Acceptance Traceability"
Cohesion: 0.67
Nodes (3): Acceptance Traceability Matrix, End-to-End Release Candidate, V1 User Journey Suite

### Community 19 - "Concurrency Invariants"
Cohesion: 0.67
Nodes (3): PostgreSQL Constraint Review, Domain Invariants Inventory, Parallel Mutation Scenarios

### Community 20 - "Task Planning and Review"
Cohesion: 0.67
Nodes (3): Acceptance Criteria and Diff Self-Review, Multi-Module Execution Plan, HackPlan Task Template

## Knowledge Gaps
- **115 isolated node(s):** `github.com/hackwerk/hackplan`, `Layered Dependency Direction`, `Product Architecture and ADR Corpus`, `Codex Tasks Skills and Acceptance Corpus`, `Prompt Package Validation` (+110 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **7 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Entscheidungen und Annahmen` connect `Product Architecture Decisions` to `Definition of Done and Backlog`?**
  _High betweenness centrality (0.057) - this node is a cross-community bridge._
- **Why does `Domain and Concurrency Review` connect `Release Reviews and Assurance` to `Scheduling Data Model`, `Domain State Machines`?**
  _High betweenness centrality (0.046) - this node is a cross-community bridge._
- **Why does `Sicherheits- und Datenschutzanforderungen` connect `Security Privacy and Configuration` to `Test Strategy and Repository`?**
  _High betweenness centrality (0.045) - this node is a cross-community bridge._
- **What connects `github.com/hackwerk/hackplan`, `Layered Dependency Direction`, `Product Architecture and ADR Corpus` to the rest of the system?**
  _115 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Release Reviews and Assurance` be split into smaller, more focused modules?**
  _Cohesion score 0.07311827956989247 - nodes in this community are weakly interconnected._
- **Should `Project Rules and Build` be split into smaller, more focused modules?**
  _Cohesion score 0.08505747126436781 - nodes in this community are weakly interconnected._
- **Should `Scheduling Data Model` be split into smaller, more focused modules?**
  _Cohesion score 0.09090909090909091 - nodes in this community are weakly interconnected._