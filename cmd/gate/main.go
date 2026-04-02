package main
import ("fmt";"log";"net/http";"os";"github.com/stockyard-dev/stockyard-gate/internal/server";"github.com/stockyard-dev/stockyard-gate/internal/store")
func main(){port:=os.Getenv("PORT");if port==""{port="8650"};dataDir:=os.Getenv("DATA_DIR");if dataDir==""{dataDir="./gate-data"}
db,err:=store.Open(dataDir);if err!=nil{log.Fatalf("gate: %v",err)};defer db.Close();srv:=server.New(db)
fmt.Printf("\n  Gate — reverse proxy and auth gateway\n  Dashboard:  http://localhost:%s/ui\n  API:        http://localhost:%s/api\n\n",port,port)
log.Printf("gate: listening on :%s",port);log.Fatal(http.ListenAndServe(":"+port,srv))}
