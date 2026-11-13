--------------------------- MODULE PublishPartialPointer ---------------------------
EXTENDS Integers, TLC



CONSTANT EXEC
CONSTANT AtomicPublish
CONSTANT RepairOnIdempotent
CONSTANT SweepPublished

VARIABLES
  state,
  pointer,
  swept

vars == <<state, pointer, swept>>

Nil == "NIL"
Present == "PRESENT"

TypeOK ==
  /\ state \in [EXEC -> {"RUNNING", "PUBLISHED"}]
  /\ pointer \in [EXEC -> {Nil, Present}]
  /\ swept \in [EXEC -> BOOLEAN]

Init ==
  /\ state = [e \in EXEC |-> "RUNNING"]
  /\ pointer = [e \in EXEC |-> Nil]
  /\ swept = [e \in EXEC |-> FALSE]

Commit(e) ==
  /\ state[e] = "RUNNING"
  /\ pointer[e] = Nil
  /\ \/ /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
       /\ pointer' = [pointer EXCEPT ![e] = Present]
     \/ /\ AtomicPublish = FALSE
       /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
       /\ UNCHANGED pointer
  /\ UNCHANGED swept

IdempotentRetry(e) ==
  /\ state[e] = "PUBLISHED"
  /\ RepairOnIdempotent = TRUE
  /\ pointer[e] = Nil
  /\ pointer' = [pointer EXCEPT ![e] = Present]
  /\ UNCHANGED <<state, swept>>

Sweep(e) ==
  /\ \/ state[e] # "PUBLISHED"
     \/ SweepPublished = TRUE
  /\ swept' = [swept EXCEPT ![e] = TRUE]
  /\ \/ /\ state[e] = "PUBLISHED"
       /\ pointer' = [pointer EXCEPT ![e] = Present]
     \/ UNCHANGED pointer
  /\ UNCHANGED state

Next ==
  \/ \E e \in EXEC : Commit(e)
  \/ \E e \in EXEC : IdempotentRetry(e)
  \/ \E e \in EXEC : Sweep(e)
  \/ UNCHANGED vars

RetryProgress == \E e \in EXEC : IdempotentRetry(e)
Progress == RetryProgress \/ \E e \in EXEC : Sweep(e)

Spec ==
  /\ Init
  /\ [][Next]_vars
  /\ WF_vars(Next)
  /\ WF_vars(RetryProgress)
  /\ WF_vars(Progress)

Agreement ==
  [](\A e \in EXEC : (state[e] = "PUBLISHED") => <>(pointer[e] = Present))

====
